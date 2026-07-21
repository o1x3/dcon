import Foundation

/// Errors surfaced by the dcon CLI bridge.
enum CLIError: LocalizedError {
    case binaryNotFound
    case failed(command: String, code: Int32, stderr: String)

    var errorDescription: String? {
        switch self {
        case .binaryNotFound:
            return "dcon binary not found. Install dcon (or set DCON_BIN) and relaunch."
        case let .failed(command, code, stderr):
            let detail = stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            return detail.isEmpty ? "`\(command)` exited with status \(code)" : detail
        }
    }
}

/// Result of a completed CLI invocation.
struct CLIResult {
    let stdout: String
    let stderr: String
    let code: Int32

    var ok: Bool { code == 0 }
}

/// Handle for a long-running, line-streamed CLI invocation (logs -f, events).
/// Cancel by calling `terminate()`; the process is also terminated on deinit.
final class StreamHandle {
    private let process: Process
    private var buffer = Data()

    fileprivate init(process: Process) {
        self.process = process
    }

    fileprivate func attach(pipe: Pipe, onLine: @escaping (String) -> Void, onEnd: @escaping () -> Void) {
        pipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            guard let self else { return }
            let data = handle.availableData
            if data.isEmpty {
                handle.readabilityHandler = nil
                // Flush any trailing partial line.
                if !self.buffer.isEmpty, let line = String(data: self.buffer, encoding: .utf8) {
                    DispatchQueue.main.async { onLine(line) }
                    self.buffer.removeAll()
                }
                DispatchQueue.main.async { onEnd() }
                return
            }
            self.buffer.append(data)
            while let nl = self.buffer.firstIndex(of: 0x0A) {
                let lineData = self.buffer.subdata(in: self.buffer.startIndex..<nl)
                self.buffer.removeSubrange(self.buffer.startIndex...nl)
                let line = String(data: lineData, encoding: .utf8) ?? ""
                DispatchQueue.main.async { onLine(line) }
            }
        }
    }

    var isRunning: Bool { process.isRunning }

    func terminate() {
        if process.isRunning { process.terminate() }
    }

    deinit { terminate() }
}

/// Bridge to the dcon CLI. Every backend interaction in the app goes through
/// here so the GUI stays byte-for-byte consistent with CLI behavior.
final class DconCLI {
    static let shared = DconCLI()

    /// Resolved path to the dcon binary. Resolution order:
    /// 1. DCON_BIN environment variable
    /// 2. dcon bundled in the app's Resources
    /// 3. Well-known install locations
    /// 4. First `dcon` on PATH
    private(set) lazy var binaryURL: URL? = Self.locateBinary()

    static func locateBinary() -> URL? {
        let fm = FileManager.default
        var candidates: [String] = []
        if let env = ProcessInfo.processInfo.environment["DCON_BIN"], !env.isEmpty {
            candidates.append(env)
        }
        if let bundled = Bundle.main.url(forResource: "dcon", withExtension: nil) {
            candidates.append(bundled.path)
        }
        candidates.append(contentsOf: [
            "/usr/local/bin/dcon",
            "/opt/homebrew/bin/dcon",
            (NSHomeDirectory() as NSString).appendingPathComponent("bin/dcon"),
        ])
        for path in candidates where fm.isExecutableFile(atPath: path) {
            return URL(fileURLWithPath: path)
        }
        // Last resort: search PATH.
        if let pathVar = ProcessInfo.processInfo.environment["PATH"] {
            for dir in pathVar.split(separator: ":") {
                let p = "\(dir)/dcon"
                if fm.isExecutableFile(atPath: p) { return URL(fileURLWithPath: p) }
            }
        }
        return nil
    }

    /// Re-run binary discovery (e.g. after the user installs dcon).
    func rediscover() {
        binaryURL = Self.locateBinary()
    }

    private func makeProcess(_ args: [String], cwd: URL?) throws -> Process {
        guard let bin = binaryURL else { throw CLIError.binaryNotFound }
        let p = Process()
        p.executableURL = bin
        p.arguments = args
        if let cwd { p.currentDirectoryURL = cwd }
        // Force plain, uncolored output; the GUI parses it.
        var env = ProcessInfo.processInfo.environment
        env["DCON_PLAIN"] = "1"
        env["NO_COLOR"] = "1"
        p.environment = env
        return p
    }

    /// Run to completion, returning stdout/stderr/exit code. Never throws on
    /// nonzero exit; inspect `result.ok`.
    func result(_ args: [String], cwd: URL? = nil) async throws -> CLIResult {
        let process = try makeProcess(args, cwd: cwd)
        let outPipe = Pipe(), errPipe = Pipe()
        process.standardOutput = outPipe
        process.standardError = errPipe
        process.standardInput = FileHandle.nullDevice

        return try await withCheckedThrowingContinuation { cont in
            do {
                try process.run()
            } catch {
                cont.resume(throwing: error)
                return
            }
            DispatchQueue.global(qos: .userInitiated).async {
                // Read fully before waitUntilExit to avoid pipe-buffer deadlock.
                let outData = outPipe.fileHandleForReading.readDataToEndOfFile()
                let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                cont.resume(returning: CLIResult(
                    stdout: String(data: outData, encoding: .utf8) ?? "",
                    stderr: String(data: errData, encoding: .utf8) ?? "",
                    code: process.terminationStatus
                ))
            }
        }
    }

    /// Run to completion; throw CLIError.failed on nonzero exit.
    @discardableResult
    func capture(_ args: [String], cwd: URL? = nil) async throws -> String {
        let r = try await result(args, cwd: cwd)
        guard r.ok else {
            throw CLIError.failed(command: "dcon " + args.joined(separator: " "), code: r.code, stderr: r.stderr)
        }
        return r.stdout
    }

    /// Run a list command that emits one JSON object per line (docker's
    /// `--format json` convention) and decode each line.
    func jsonLines<T: Decodable>(_ type: T.Type, _ args: [String]) async throws -> [T] {
        let out = try await capture(args)
        return Self.decodeJSONLines(type, from: out)
    }

    /// Decode newline-delimited JSON, skipping blank/undecodable lines.
    static func decodeJSONLines<T: Decodable>(_ type: T.Type, from text: String) -> [T] {
        let decoder = JSONDecoder()
        var out: [T] = []
        for line in text.split(separator: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            guard trimmed.hasPrefix("{"), let data = trimmed.data(using: .utf8) else { continue }
            if let v = try? decoder.decode(type, from: data) { out.append(v) }
        }
        return out
    }

    /// Start a streaming invocation (logs --follow, system events). Lines are
    /// delivered on the main queue. stderr is merged into the stream.
    func stream(_ args: [String], cwd: URL? = nil,
                onLine: @escaping (String) -> Void,
                onEnd: @escaping () -> Void = {}) throws -> StreamHandle {
        let process = try makeProcess(args, cwd: cwd)
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        process.standardInput = FileHandle.nullDevice
        let handle = StreamHandle(process: process)
        handle.attach(pipe: pipe, onLine: onLine, onEnd: onEnd)
        try process.run()
        return handle
    }
}
