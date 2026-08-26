// Package grafana validates the provisioned "Weather Nerd" dashboard JSON
// against Contract B (design §13 / the OTel writer's exact stormglass_* metric
// names). It has no runtime dependency on the dashboard — it is a static
// guard so the JSON can never silently drift from the metric names the
// collector actually emits.
package grafana

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"
)

// contractBMetrics is the authoritative set of stormglass_* metric names the
// OTel writer emits (Contract B, right column; mirrors design §13 and
// internal/otel/promnames_test.go from Task 4.1). Any token extracted from a
// panel's PromQL expr that starts with "stormglass_" and is not in this set is
// a defect: the dashboard is referencing a metric name the exporter does not
// produce.
var contractBMetrics = []string{
	"stormglass_temperature_c",
	"stormglass_dewpoint_c",
	"stormglass_heat_index_c",
	"stormglass_wetbulb_c",
	"stormglass_humidity_percent",
	"stormglass_pressure_mb",
	"stormglass_wind_meters_per_second",
	"stormglass_wind_direction_degrees",
	"stormglass_uv_index",
	"stormglass_irradiance_w_m2",
	"stormglass_illuminance_lux",
	"stormglass_rain_rate_mm_min",
	"stormglass_rainfall_mm_total",
	"stormglass_lightning_distance_km",
	"stormglass_lightning_strike_count_total",
	"stormglass_battery_volts",
	"stormglass_rssi_dbm",
	"stormglass_uptime_seconds",
	"stormglass_reboots_total",
	"stormglass_bus_errors_total",
}

// stormglassTokenRE extracts every stormglass_* identifier from a PromQL expr.
var stormglassTokenRE = regexp.MustCompile(`stormglass_[a-z0-9_]+`)

// dashboard is a deliberately loose model of the Grafana dashboard JSON
// schema: only the fields the validator needs to inspect.
type dashboard struct {
	Panels     []panel `json:"panels"`
	Templating struct {
		List []templateVar `json:"list"`
	} `json:"templating"`
}

// panel models a single Grafana panel. Panels may nest (Grafana row-type
// panels carry their child panels in their own "panels" array when
// collapsed), so the walker recurses into Panels wherever present.
type panel struct {
	Title   string   `json:"title"`
	Type    string   `json:"type"`
	Targets []target `json:"targets"`
	Panels  []panel  `json:"panels"`
}

type target struct {
	Expr  string `json:"expr"`
	RefID string `json:"refId"`
}

type templateVar struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

const dashboardPath = "dashboards/weather-nerd.json"

func loadDashboard(t *testing.T) dashboard {
	t.Helper()
	raw, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatalf("reading %s: %v", dashboardPath, err)
	}
	var d dashboard
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshaling %s: %v", dashboardPath, err)
	}
	return d
}

// walkPanels calls fn for every panel in the tree, including panels nested
// inside row-type panels.
func walkPanels(panels []panel, fn func(panel)) {
	for _, p := range panels {
		fn(p)
		if len(p.Panels) > 0 {
			walkPanels(p.Panels, fn)
		}
	}
}

// TestDashboardExprsReferenceOnlyContractBMetrics walks every panel's
// targets and asserts every stormglass_* token extracted from expr is a member
// of Contract B. A metric name outside the set means the dashboard queries a
// series the exporter never produces.
func TestDashboardExprsReferenceOnlyContractBMetrics(t *testing.T) {
	d := loadDashboard(t)

	found := false
	walkPanels(d.Panels, func(p panel) {
		for _, tgt := range p.Targets {
			if tgt.Expr == "" {
				continue
			}
			for _, tok := range stormglassTokenRE.FindAllString(tgt.Expr, -1) {
				found = true
				if !slices.Contains(contractBMetrics, tok) {
					t.Errorf("panel %q (target %s): expr references metric %q, which is not in Contract B\n  expr: %s",
						p.Title, tgt.RefID, tok, tgt.Expr)
				}
			}
		}
	})

	if !found {
		t.Fatal("no stormglass_* metric tokens found in any panel expr — dashboard has no queries")
	}
}

// TestSerialTemplateVariableExists asserts the $serial template variable is
// defined as a query variable, per design §13.
func TestSerialTemplateVariableExists(t *testing.T) {
	d := loadDashboard(t)

	for _, v := range d.Templating.List {
		if v.Name == "serial" && v.Type == "query" {
			return
		}
	}
	t.Fatal(`templating.list does not contain a "serial" query variable`)
}

// TestRangeBuiltinUsedInPanelExpr asserts $__range (Grafana's built-in
// dashboard-range global — it must NOT appear in templating.list as a
// defined variable) is actually referenced by at least one panel expr, per
// the Row 8 records/almanac panels in design §13.
func TestRangeBuiltinUsedInPanelExpr(t *testing.T) {
	d := loadDashboard(t)

	for _, v := range d.Templating.List {
		if v.Name == "__range" {
			t.Fatal(`"__range" must not be a defined templating.list variable — it is a Grafana built-in`)
		}
	}

	used := false
	walkPanels(d.Panels, func(p panel) {
		for _, tgt := range p.Targets {
			if regexp.MustCompile(`\$__range\b`).MatchString(tgt.Expr) {
				used = true
			}
		}
	})
	if !used {
		t.Fatal("no panel expr references $__range — expected the Row 8 records/almanac panels to use it")
	}
}
