import XCTest
@testable import Dcon

final class ModelsTests: XCTestCase {
    func testDecodeContainerRows() {
        let out = """
        {"ID":"abc123def456","Image":"nginx:latest","Command":"\\"nginx -g …\\"","CreatedAt":"2026-07-20 10:00:00 +0000 UTC","RunningFor":"2 hours ago","Status":"Up 2 hours","Ports":"0.0.0.0:8080->80/tcp","Names":"web","Labels":"","Mounts":"","Networks":"default","Size":"","State":"running","LocalVolumes":"0"}
        {"ID":"ffff0000aaaa","Image":"alpine","Command":"\\"sh\\"","CreatedAt":"","RunningFor":"","Status":"Exited (0)","Ports":"","Names":"tmp","Labels":"","Mounts":"","Networks":"","Size":"","State":"exited","LocalVolumes":"0"}
        """
        let rows = DconCLI.decodeJSONLines(ContainerRow.self, from: out)
        XCTAssertEqual(rows.count, 2)
        XCTAssertEqual(rows[0].Names, "web")
        XCTAssertTrue(rows[0].isRunning)
        XCTAssertFalse(rows[1].isRunning)
        XCTAssertEqual(rows[0].shortID, "abc123def456")
    }

    func testDecodeImageRows() {
        let out = """
        {"Repository":"nginx","Tag":"latest","ID":"sha256aaaa","Digest":"sha256:xyz","CreatedSince":"3 days ago","CreatedAt":"2026-07-17","Size":"70MB","Platform":"linux/arm64"}
        """
        let rows = DconCLI.decodeJSONLines(ImageRow.self, from: out)
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].reference, "nginx:latest")
    }

    func testDecodeSkipsGarbage() {
        let out = "warning: something\n{\"Name\":\"vol1\",\"Driver\":\"local\",\"Scope\":\"local\",\"Mountpoint\":\"/x\",\"Labels\":\"\"}\n"
        let rows = DconCLI.decodeJSONLines(VolumeRow.self, from: out)
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].Name, "vol1")
    }

    func testParseWarmLs() async {
        let out = """
        CONTAINER ID   IMAGE           AGE   STATE
        1a2b3c4d5e6f   alpine:latest   30s   ready
        (pool empty)
        """
        let rows = await AppState.parseWarmLs(out)
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].image, "alpine:latest")
        XCTAssertEqual(rows[0].state, "ready")
    }
}
