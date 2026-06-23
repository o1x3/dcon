class Dcon < Formula
  desc "Drop-in Docker CLI for macOS, powered by Apple's container runtime"
  homepage "https://github.com/o1x3/dcon"
  version "1.0.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/o1x3/dcon/releases/download/v1.0.0/dcon_1.0.0_darwin_arm64.tar.gz"
      sha256 "9bae1645cd6de05c614b4050e36e6d53a48c0d28eeaeca888b9534639d7c1032"
    end
    on_intel do
      url "https://github.com/o1x3/dcon/releases/download/v1.0.0/dcon_1.0.0_darwin_amd64.tar.gz"
      sha256 "6fdba1a28fe5ed12f217aaf7011bf14150c039ec8491476b44e3d9864a97d420"
    end
  end

  def install
    bin.install "dcon"
    generate_completions_from_executable(bin/"dcon", "completion")
  end

  def caveats
    <<~EOS
      dcon needs Apple's `container` runtime: https://github.com/apple/container

      First-time setup:
        dcon system start
        dcon system kernel set --recommended
    EOS
  end

  test do
    assert_match "dcon version", shell_output("#{bin}/dcon --version")
  end
end
