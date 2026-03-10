package selfupdate

import "testing"

func TestAssetNameFor(t *testing.T) {
	t.Run("linux amd64", func(t *testing.T) {
		got, err := assetNameFor("linux", "amd64")
		if err != nil {
			t.Fatalf("assetNameFor returned error: %v", err)
		}
		if got != "xray-agent_linux_amd64" {
			t.Fatalf("unexpected asset name: %s", got)
		}
	})

	t.Run("unsupported arch", func(t *testing.T) {
		if _, err := assetNameFor("linux", "386"); err == nil {
			t.Fatal("expected unsupported arch error")
		}
	})
}

func TestParseChecksum(t *testing.T) {
	const asset = "xray-agent_linux_amd64"
	want := "d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2"

	t.Run("sha256sum format", func(t *testing.T) {
		input := []byte(want + "  " + asset + "\n")
		got := parseChecksum(input, asset)
		if got != want {
			t.Fatalf("parseChecksum() = %q, want %q", got, want)
		}
	})

	t.Run("openssl format", func(t *testing.T) {
		input := []byte("SHA256 (" + asset + ") = " + want + "\n")
		got := parseChecksum(input, asset)
		if got != want {
			t.Fatalf("parseChecksum() = %q, want %q", got, want)
		}
	})
}
