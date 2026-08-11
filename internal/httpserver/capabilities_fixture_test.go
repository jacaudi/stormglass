package httpserver

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

// updateFixture rewrites the committed fixture instead of asserting against
// it (go-standards §8.1's golden-file convention). Normal `go test` runs only
// read the file, so the suite never writes to the working tree unbidden.
var updateFixture = flag.Bool("update", false, "rewrite the committed capabilities fixture")

// capabilitiesFixturePath lives under web/src because web/tsconfig.app.json's
// include is ["src"] and Vite's server.fs.allow would fight an import from
// outside the web/ root. It is the ONE artifact both languages pin to: this
// test owns its content, and web/src/types/capabilities.contract.test.ts
// assigns it to the Capabilities interface. A key renamed on either side
// fails one of the two.
const capabilitiesFixturePath = "../../web/src/types/__fixtures__/capabilities.json"

func TestCapabilities_GoldenFixture(t *testing.T) {
	got, err := json.Marshal(newCapabilities(Deps{}))
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}

	if *updateFixture {
		if err := os.WriteFile(capabilitiesFixturePath, append(got, '\n'), 0o600); err != nil {
			t.Fatalf("update fixture %s: %v", capabilitiesFixturePath, err)
		}
		t.Logf("updated %s", capabilitiesFixturePath)
	}

	want, err := os.ReadFile(capabilitiesFixturePath)
	if err != nil {
		t.Fatalf("read fixture %s (run `go test ./internal/httpserver/ -run TestCapabilities_GoldenFixture -update` to create it): %v",
			capabilitiesFixturePath, err)
	}

	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got)) {
		t.Errorf("capabilities wire format drifted from the committed fixture.\n got: %s\nwant: %s\n"+
			"If this change is intended, re-run with -update AND check whether "+
			"web/src/types/weather.ts's Capabilities interface needs the same change.",
			bytes.TrimSpace(got), bytes.TrimSpace(want))
	}
}
