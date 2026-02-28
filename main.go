package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/components"
	"github.com/go-echarts/go-echarts/v2/opts"
)

type MarketData struct {
	Date    string  `json:"Date"`
	OMXSPI  float64 `json:"OMXSPI"`
	SX20PI  float64 `json:"SX20PI"`  // Health Care (Recession Defense)
	SX30PI  float64 `json:"SX30PI"`  // Financials/Banks (High-Yield Haven)
	SX35PI  float64 `json:"SX35PI"`  // Real Estate (Rate Cut Play)
	SX50PI  float64 `json:"SX50PI"`  // Industrials (Export Recovery)
	Yield2Y  float64 `json:"Yield2Y"`
	Yield10Y float64 `json:"Yield10Y"`
}

type LLMOutput struct {
	Metadata         Metadata          `json:"metadata"`
	Rankings         []Ranking         `json:"rankings"`
	Momentum         []Momentum        `json:"momentum"`
	CrossoverSignals []CrossoverSignal `json:"crossover_signals"`
	Regime           RegimeSummary     `json:"regime"`
}

type Metadata struct {
	Date        string `json:"date"`
	Benchmark   string `json:"benchmark"`
	Description string `json:"description"`
}

type Ranking struct {
	Sector  string  `json:"sector"`
	RSValue float64 `json:"rs_value"`
	Rank    int     `json:"rank"`
}

type Momentum struct {
	Sector   string  `json:"sector"`
	Change1W float64 `json:"1w_change"`
	Change1M float64 `json:"1m_change"`
	Trend    string  `json:"trend"`
}

type CrossoverSignal struct {
	Sectors     []string `json:"sectors"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
}

type RegimeSummary struct {
	YieldEnv       string `json:"yield_environment"`
	SpreadStatus   string `json:"spread_status"`
	StrongestSector string `json:"strongest_sector"`
	WeakestSector  string `json:"weakest_sector"`
	RotationSignal string `json:"rotation_signal"`
}

type RiksbankObs struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

var sectorNames = map[string]string{
	"SX50": "Industrials",
	"SX35": "Real Estate",
	"SX30": "Banks",
	"SX20": "Health Care",
}

func main() {
	fmt.Println("Starting Sector Rotation Radar...")
	dataSeries := fetchRealMarketData()
	if len(dataSeries) == 0 {
		fmt.Println("No data available. Exiting.")
		return
	}
	generateRotationDashboard(dataSeries)
	exportLLMSignals(dataSeries)
}

func fetchRealMarketData() []MarketData {
	filename := "market_data.json"

	var existingData []MarketData
	if dataBytes, err := os.ReadFile(filename); err == nil {
		json.Unmarshal(dataBytes, &existingData)
	}

	marketDataMap := make(map[string]MarketData)
	for _, data := range existingData {
		marketDataMap[data.Date] = data
	}

	seriesOMX := parseCSVPrice("omxspi*.csv")
	seriesSX20 := parseCSVPrice("sx20pi*.csv")
	seriesSX30 := parseCSVPrice("sx30pi*.csv")
	seriesSX35 := parseCSVPrice("sx35pi*.csv")
	seriesSX50 := parseCSVPrice("sx50pi*.csv")

	allNewDates := make(map[string]bool)
	seriesList := []map[string]float64{seriesOMX, seriesSX20, seriesSX30, seriesSX35, seriesSX50}
	for _, series := range seriesList {
		for d := range series {
			allNewDates[d] = true
		}
	}

	for dateStr := range allNewDates {
		md := marketDataMap[dateStr]
		md.Date = dateStr
		if val, ok := seriesOMX[dateStr]; ok { md.OMXSPI = val }
		if val, ok := seriesSX20[dateStr]; ok { md.SX20PI = val }
		if val, ok := seriesSX30[dateStr]; ok { md.SX30PI = val }
		if val, ok := seriesSX35[dateStr]; ok { md.SX35PI = val }
		if val, ok := seriesSX50[dateStr]; ok { md.SX50PI = val }
		marketDataMap[dateStr] = md
	}

	// Fetch real bond yields from Riksbank
	var minDate, maxDate string
	for d := range marketDataMap {
		if minDate == "" || d < minDate { minDate = d }
		if maxDate == "" || d > maxDate { maxDate = d }
	}

	if minDate != "" && maxDate != "" {
		fmt.Printf("Fetching Riksbank bond yields (%s to %s)...\n", minDate, maxDate)
		yield2Y := fetchRiksbankYield("SEGVB2YC", minDate, maxDate)
		yield10Y := fetchRiksbankYield("SEGVB10YC", minDate, maxDate)

		for dateStr, val := range yield2Y {
			if md, ok := marketDataMap[dateStr]; ok {
				md.Yield2Y = val
				marketDataMap[dateStr] = md
			}
		}
		for dateStr, val := range yield10Y {
			if md, ok := marketDataMap[dateStr]; ok {
				md.Yield10Y = val
				marketDataMap[dateStr] = md
			}
		}
		fmt.Printf("  2Y: %d data points, 10Y: %d data points\n", len(yield2Y), len(yield10Y))
	}

	var finalData []MarketData
	for _, md := range marketDataMap {
		finalData = append(finalData, md)
	}
	sort.Slice(finalData, func(i, j int) bool {
		return finalData[i].Date < finalData[j].Date
	})

	if dataBytes, err := json.MarshalIndent(finalData, "", "  "); err == nil {
		os.WriteFile(filename, dataBytes, 0644)
	}

	return finalData
}

func fetchRiksbankYield(seriesID, fromDate, toDate string) map[string]float64 {
	result := make(map[string]float64)
	url := fmt.Sprintf("https://api.riksbank.se/swea/v1/Observations/%s/%s/%s", seriesID, fromDate, toDate)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("  Warning: Failed to fetch %s: %v\n", seriesID, err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("  Warning: Riksbank API returned %d for %s\n", resp.StatusCode, seriesID)
		return result
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result
	}

	var observations []RiksbankObs
	if err := json.Unmarshal(body, &observations); err != nil {
		fmt.Printf("  Warning: Failed to parse %s response: %v\n", seriesID, err)
		return result
	}

	for _, obs := range observations {
		result[obs.Date] = obs.Value
	}
	return result
}

func parseCSVPrice(pattern string) map[string]float64 {
	res := make(map[string]float64)

	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		matches, err = filepath.Glob("history/" + pattern)
	}
	if err != nil || len(matches) == 0 {
		fmt.Printf("  No CSV files matching: %s\n", pattern)
		return res
	}

	for _, filename := range matches {
		file, err := os.Open(filename)
		if err != nil {
			fmt.Printf("  Cannot read file %s: %v\n", filename, err)
			continue
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "sep=") || strings.HasPrefix(line, "Date") {
				continue
			}

			parts := strings.Split(line, ";")
			if len(parts) >= 4 {
				dateStr := parts[0]
				closeStr := strings.ReplaceAll(parts[3], "\"", "")
				closeStr = strings.ReplaceAll(closeStr, ",", "")

				closePrice, err := strconv.ParseFloat(closeStr, 64)
				if err == nil && closePrice > 0 {
					res[dateStr] = closePrice
				}
			}
		}
		file.Close()
	}

	return res
}

func safeRS(sector, benchmark float64) interface{} {
	if benchmark == 0 || math.IsNaN(benchmark) || math.IsInf(benchmark, 0) {
		return "-"
	}
	val := (sector / benchmark) * 100
	if math.IsNaN(val) || math.IsInf(val, 0) || val == 0 {
		return "-"
	}
	return val
}

func generateRotationDashboard(data []MarketData) {
	if len(data) == 0 {
		return
	}

	// --- RS Chart ---
	var dates []string
	var rs20, rs30, rs35, rs50 []opts.LineData

	for _, d := range data {
		dates = append(dates, d.Date)
		rs20 = append(rs20, opts.LineData{Value: safeRS(d.SX20PI, d.OMXSPI)})
		rs30 = append(rs30, opts.LineData{Value: safeRS(d.SX30PI, d.OMXSPI)})
		rs35 = append(rs35, opts.LineData{Value: safeRS(d.SX35PI, d.OMXSPI)})
		rs50 = append(rs50, opts.LineData{Value: safeRS(d.SX50PI, d.OMXSPI)})
	}

	rsChart := charts.NewLine()
	rsChart.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{Width: "1200px", Height: "500px"}),
		charts.WithTitleOpts(opts.Title{
			Title:    "Core Arena: 4 Super-Sector Relative Strength (RS Line)",
			Subtitle: "Watch for crossovers: upward = capital inflow, downward = retreat",
			Top:      "2%",
		}),
		charts.WithGridOpts(opts.Grid{Top: "25%", Right: "18%", Bottom: "10%", Left: "8%"}),
		charts.WithLegendOpts(opts.Legend{Right: "0%", Top: "25%", Orient: "vertical"}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithYAxisOpts(opts.YAxis{Name: "RS Strength", Scale: opts.Bool(true)}),
		charts.WithColorsOpts([]string{"#fac858", "#ee6666", "#91cc75", "#73c0de"}),
	)
	rsChart.SetXAxis(dates).
		AddSeries("SX50 Industrials (Export Recovery)", rs50).
		AddSeries("SX35 Real Estate (Rate Cut Play)", rs35).
		AddSeries("SX30 Banks (High-Yield Haven)", rs30).
		AddSeries("SX20 Health Care (Recession Defense)", rs20).
		SetSeriesOptions(charts.WithLineChartOpts(opts.LineChart{Smooth: opts.Bool(true)}))

	// --- Yield Chart ---
	var yieldDates []string
	var y2Data, y10Data []opts.LineData
	for _, d := range data {
		if d.Yield2Y > 0 || d.Yield10Y > 0 {
			yieldDates = append(yieldDates, d.Date)
			y2Data = append(y2Data, opts.LineData{Value: d.Yield2Y})
			y10Data = append(y10Data, opts.LineData{Value: d.Yield10Y})
		}
	}

	yieldChart := charts.NewLine()
	yieldChart.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{Width: "1200px", Height: "400px"}),
		charts.WithTitleOpts(opts.Title{
			Title: "Macro Anchor: Sweden 2Y & 10Y Government Bond Yield (%)",
			Top:   "2%",
		}),
		charts.WithGridOpts(opts.Grid{Top: "25%", Right: "18%", Bottom: "10%", Left: "8%"}),
		charts.WithLegendOpts(opts.Legend{Right: "0%", Top: "25%", Orient: "vertical"}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithYAxisOpts(opts.YAxis{Name: "Yield %", Scale: opts.Bool(true)}),
		charts.WithColorsOpts([]string{"#d50000", "#1e88e5"}),
	)
	yieldChart.SetXAxis(yieldDates).
		AddSeries("2Y Yield", y2Data).
		AddSeries("10Y Yield", y10Data).
		SetSeriesOptions(charts.WithLineChartOpts(opts.LineChart{Smooth: opts.Bool(true)}))

	// --- Spread Chart ---
	var spreadData []opts.LineData
	for _, d := range data {
		if d.Yield2Y > 0 || d.Yield10Y > 0 {
			spread := d.Yield10Y - d.Yield2Y
			spreadData = append(spreadData, opts.LineData{Value: math.Round(spread*1000) / 1000})
		}
	}

	spreadChart := charts.NewLine()
	spreadChart.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{Width: "1200px", Height: "400px"}),
		charts.WithTitleOpts(opts.Title{
			Title: "Yield Curve Spread (10Y - 2Y): Recession / Recovery Gauge",
			Top:   "2%",
		}),
		charts.WithGridOpts(opts.Grid{Top: "25%", Right: "18%", Bottom: "10%", Left: "8%"}),
		charts.WithLegendOpts(opts.Legend{Right: "0%", Top: "25%", Orient: "vertical"}),
		charts.WithTooltipOpts(opts.Tooltip{Show: opts.Bool(true), Trigger: "axis"}),
		charts.WithYAxisOpts(opts.YAxis{Name: "Spread %", Scale: opts.Bool(true)}),
		charts.WithColorsOpts([]string{"#4caf50"}),
	)
	spreadChart.SetXAxis(yieldDates).
		AddSeries("10Y - 2Y Spread", spreadData).
		SetSeriesOptions(
			charts.WithLineChartOpts(opts.LineChart{Smooth: opts.Bool(true)}),
			charts.WithAreaStyleOpts(opts.AreaStyle{}),
		)

	// --- Render ---
	page := components.NewPage()
	page.SetLayout(components.PageNoneLayout)
	page.AddCharts(rsChart, yieldChart, spreadChart)

	filename := "rotation_dashboard.html"
	f, _ := os.Create(filename)
	defer f.Close()
	page.Render(f)

	injectDashboardEnhancements(filename, data)

	fmt.Printf("Dashboard generated: %s\n", filename)
}

// buildRegimeSummary analyzes the regime over a given lookback window.
// periodLabel is "1Y", "1M", or "1W"; lookbackDays is the number of trading days to look back.
func buildRegimeSummary(data []MarketData, periodLabel string, lookbackDays int) RegimeSummary {
	regime := RegimeSummary{}
	if len(data) < 6 {
		return regime
	}

	last := data[len(data)-1]
	prevIdx := len(data) - min(lookbackDays+1, len(data))
	prev := data[prevIdx]

	periodDesc := map[string]string{
		"1Y": "over the past year",
		"1M": "over the past month",
		"1W": "over the past week",
	}[periodLabel]

	// Yield environment
	if last.Yield2Y > 0 && prev.Yield2Y > 0 {
		diff2Y := last.Yield2Y - prev.Yield2Y
		thresholdMap := map[string]float64{"1Y": 0.3, "1M": 0.1, "1W": 0.03}
		threshold := thresholdMap[periodLabel]

		if diff2Y > threshold {
			regime.YieldEnv = fmt.Sprintf("Rising short rates %s (2Y: %.2f%%, %+.0fbps) — tightening bias, headwind for rate-sensitive assets", periodDesc, last.Yield2Y, diff2Y*100)
		} else if diff2Y < -threshold {
			regime.YieldEnv = fmt.Sprintf("Falling short rates %s (2Y: %.2f%%, %.0fbps) — easing signal, tailwind for Real Estate & growth", periodDesc, last.Yield2Y, diff2Y*100)
		} else {
			regime.YieldEnv = fmt.Sprintf("Stable short rates %s (2Y: %.2f%%, %+.0fbps) — neutral monetary stance", periodDesc, last.Yield2Y, diff2Y*100)
		}
	}

	// Spread status
	if last.Yield2Y > 0 && last.Yield10Y > 0 {
		spread := last.Yield10Y - last.Yield2Y
		prevSpread := prev.Yield10Y - prev.Yield2Y
		spreadChange := spread - prevSpread
		thresholdMap := map[string]float64{"1Y": 0.15, "1M": 0.05, "1W": 0.02}
		threshold := thresholdMap[periodLabel]

		if spread < 0 {
			regime.SpreadStatus = fmt.Sprintf("INVERTED (%.0fbps) — recession warning, favor Health Care (SX20)", spread*100)
		} else if spreadChange > threshold {
			regime.SpreadStatus = fmt.Sprintf("STEEPENING %s (+%.0fbps, was +%.0fbps) — recovery expectations rising, favor Industrials (SX50) & Banks (SX30)", periodDesc, spread*100, prevSpread*100)
		} else if spreadChange < -threshold {
			regime.SpreadStatus = fmt.Sprintf("FLATTENING %s (+%.0fbps, was +%.0fbps) — late-cycle caution, reduce risk exposure", periodDesc, spread*100, prevSpread*100)
		} else {
			regime.SpreadStatus = fmt.Sprintf("STABLE %s (+%.0fbps) — steady macro backdrop", periodDesc, spread*100)
		}
	}

	// Sector RS momentum
	sectors := []string{"SX50", "SX35", "SX30", "SX20"}
	bestSector, worstSector := "", ""
	bestMom, worstMom := -999.0, 999.0

	for _, sec := range sectors {
		rsNow := getSectorRS(last, sec)
		rsPrev := getSectorRS(prev, sec)
		if rsPrev > 0 {
			mom := (rsNow - rsPrev) / rsPrev * 100
			if mom > bestMom {
				bestMom = mom
				bestSector = sec
			}
			if mom < worstMom {
				worstMom = mom
				worstSector = sec
			}
		}
	}

	if bestSector != "" {
		regime.StrongestSector = fmt.Sprintf("%s %s (%+.1f%% RS %s)", bestSector, sectorNames[bestSector], bestMom, periodLabel)
	}
	if worstSector != "" {
		regime.WeakestSector = fmt.Sprintf("%s %s (%+.1f%% RS %s)", worstSector, sectorNames[worstSector], worstMom, periodLabel)
	}

	// Rotation signal
	if bestSector != "" && worstSector != "" {
		regime.RotationSignal = describeRotation(bestSector, worstSector, last)
	}

	return regime
}

func getSectorRS(d MarketData, sector string) float64 {
	if d.OMXSPI <= 0 { return 0 }
	switch sector {
	case "SX50": return (d.SX50PI / d.OMXSPI) * 100
	case "SX35": return (d.SX35PI / d.OMXSPI) * 100
	case "SX30": return (d.SX30PI / d.OMXSPI) * 100
	case "SX20": return (d.SX20PI / d.OMXSPI) * 100
	}
	return 0
}

func describeRotation(strong, weak string, d MarketData) string {
	spread := d.Yield10Y - d.Yield2Y

	switch {
	case strong == "SX35" && (weak == "SX30" || weak == "SX20"):
		return "Rate-cut rotation: Capital flowing into Real Estate as yields drop. Classic easing-cycle play."
	case strong == "SX50" && weak == "SX20":
		return "Risk-on rotation: Capital shifting from defensive Health Care to cyclical Industrials. Recovery/expansion signal."
	case strong == "SX30" && weak == "SX35":
		return "Yield-curve play: Banks gaining from wider margins while Real Estate suffers from rate pressure."
	case strong == "SX20" && (weak == "SX50" || weak == "SX35"):
		return "Defensive rotation: Capital retreating to Health Care. Risk-off / late-cycle positioning."
	case strong == "SX50" && weak == "SX35":
		return "Global growth > domestic rates: Industrials leading on export demand despite rate headwinds for Real Estate."
	case strong == "SX30" && weak == "SX20":
		return "Value rotation: Banks gaining on yield advantage, defensive sectors losing relative appeal."
	case spread < 0:
		return "Curve inverted — macro stress regime. Watch for defensive sector leadership."
	default:
		return fmt.Sprintf("Capital rotating from %s %s toward %s %s.", weak, sectorNames[weak], strong, sectorNames[strong])
	}
}

func injectDashboardEnhancements(filename string, data []MarketData) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return
	}

	htmlContent := string(content)

	if !strings.Contains(htmlContent, "<html>") {
		htmlContent = "<html>\n<head>\n</head>\n<body>\n" + htmlContent + "\n</body>\n</html>"
	}
	if !strings.Contains(htmlContent, "<head") {
		htmlContent = strings.Replace(htmlContent, "<html>", "<html>\n<head></head>", 1)
	}
	if !strings.Contains(htmlContent, "<body") {
		htmlContent = strings.Replace(htmlContent, "</head>", "</head>\n<body>", 1)
		htmlContent += "\n</body>"
	}

	regime1Y := buildRegimeSummary(data, "1Y", 252)
	regime1M := buildRegimeSummary(data, "1M", 22)
	regime1W := buildRegimeSummary(data, "1W", 5)

	css := `
<style>
* { box-sizing: border-box; }
body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f5f5f5; }
.controls {
    padding: 16px 24px; background: #1a1a2e;
    display: flex; gap: 12px; align-items: center;
    position: sticky; top: 0; z-index: 1000;
    box-shadow: 0 2px 8px rgba(0,0,0,0.3);
}
.btn {
    padding: 8px 18px; border: 1px solid rgba(255,255,255,0.3); background: transparent;
    color: rgba(255,255,255,0.8); border-radius: 6px; cursor: pointer;
    font-weight: 500; font-size: 13px; transition: all 0.2s;
}
.btn:hover { background: rgba(255,255,255,0.1); color: white; }
.btn.active { background: #4361ee; color: white; border-color: #4361ee; }
.page-title { margin: 0; font-size: 1.1rem; color: white; margin-right: 24px; letter-spacing: 0.5px; }
.page-date { color: rgba(255,255,255,0.5); font-size: 12px; margin-left: auto; }
.regime-panel {
    background: white; border-radius: 12px; margin: 20px 24px;
    padding: 24px; box-shadow: 0 1px 4px rgba(0,0,0,0.08);
}
.regime-panel h3 { margin: 0 0 16px 0; font-size: 15px; color: #1a1a2e; text-transform: uppercase; letter-spacing: 1px; }
.regime-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.regime-card {
    padding: 16px; border-radius: 8px; border-left: 4px solid #ccc;
}
.regime-card.yield { border-color: #d50000; background: #fff5f5; }
.regime-card.spread { border-color: #1e88e5; background: #f0f7ff; }
.regime-card.strong { border-color: #2e7d32; background: #f0faf0; }
.regime-card.weak { border-color: #ff6f00; background: #fff8f0; }
.regime-card .label { font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px; color: #666; margin-bottom: 6px; }
.regime-card .value { font-size: 14px; font-weight: 600; color: #333; line-height: 1.4; }
.rotation-signal {
    margin-top: 16px; padding: 16px 20px; background: #1a1a2e; color: white;
    border-radius: 8px; font-size: 14px; line-height: 1.5;
}
.rotation-signal .label { font-size: 11px; text-transform: uppercase; letter-spacing: 1px; color: rgba(255,255,255,0.5); margin-bottom: 6px; }
.guide-panel {
    background: white; border-radius: 12px; margin: 0 24px 20px 24px;
    padding: 20px 24px; font-size: 13px; line-height: 1.6; color: #555;
    box-shadow: 0 1px 4px rgba(0,0,0,0.08);
}
.guide-panel h4 { margin: 0 0 12px 0; color: #1a1a2e; font-size: 14px; }
.guide-panel details { margin-bottom: 8px; }
.guide-panel summary { cursor: pointer; font-weight: 600; color: #333; padding: 4px 0; font-size: 13px; }
.guide-panel ul { margin: 6px 0 0 0; padding-left: 20px; }
.guide-panel li { margin-bottom: 3px; }
.signal-up { color: #2e7d32; font-weight: bold; }
.signal-down { color: #c62828; font-weight: bold; }
.container { padding: 0 12px; }
.regime-panel { display: none; }
.regime-panel.active { display: block; }
.tips-panel {
    background: #1a1a2e; border-radius: 12px;
    margin: 0 24px 20px 24px; padding: 24px;
    box-shadow: 0 2px 8px rgba(0,0,0,0.15);
    position: relative; z-index: 1;
}
.tips-panel h4 { margin: 0 0 16px 0; font-size: 14px; color: #4cc9f0; text-transform: uppercase; letter-spacing: 1px; }
.tips-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.tip-card {
    padding: 14px 16px; border-radius: 8px; background: #232946;
    border: 1px solid #2e3a5e; font-size: 13px; line-height: 1.5;
}
.tip-card .tip-title { font-weight: 700; color: #4cc9f0; margin-bottom: 4px; font-size: 12px; text-transform: uppercase; letter-spacing: 0.5px; }
.tip-card .tip-body { color: #b8c1d8; }
</style>`

	lastDate := ""
	if len(data) > 0 {
		lastDate = data[len(data)-1].Date
	}

	buildRegimeHTML := func(id, periodLabel string, regime RegimeSummary, active bool) string {
		if regime.YieldEnv == "" {
			return ""
		}
		activeClass := ""
		if active {
			activeClass = " active"
		}
		return fmt.Sprintf(`
<div class="regime-panel%s" id="regime-%s">
    <h3>Market Regime &mdash; %s View &mdash; %s</h3>
    <div class="regime-grid">
        <div class="regime-card yield">
            <div class="label">Yield Environment</div>
            <div class="value">%s</div>
        </div>
        <div class="regime-card spread">
            <div class="label">Curve Spread</div>
            <div class="value">%s</div>
        </div>
        <div class="regime-card strong">
            <div class="label">Strongest Momentum</div>
            <div class="value">%s</div>
        </div>
        <div class="regime-card weak">
            <div class="label">Weakest Momentum</div>
            <div class="value">%s</div>
        </div>
    </div>
    <div class="rotation-signal">
        <div class="label">Rotation Signal</div>
        %s
    </div>
</div>`, activeClass, id, periodLabel, lastDate,
			regime.YieldEnv, regime.SpreadStatus, regime.StrongestSector, regime.WeakestSector, regime.RotationSignal)
	}

	regimeHTML := buildRegimeHTML("1Y", "1 Year (Macro Cycle)", regime1Y, true) +
		buildRegimeHTML("1M", "1 Month (Trend)", regime1M, false) +
		buildRegimeHTML("1W", "1 Week (Momentum)", regime1W, false)

	controls := fmt.Sprintf(`
<div class="controls">
    <h3 class="page-title">Sector Rotation Radar</h3>
    <button class="btn active" onclick="updateTimeframe('1Y', this)">1 Year</button>
    <button class="btn" onclick="updateTimeframe('1M', this)">1 Month</button>
    <button class="btn" onclick="updateTimeframe('1W', this)">1 Week</button>
    <span class="page-date">Last data: %s</span>
</div>
%s`, lastDate, regimeHTML)

	guide := `
<div class="tips-panel">
    <h4>Macro Playbook &mdash; Swedish Market</h4>
    <div class="tips-grid">
        <div class="tip-card">
            <div class="tip-title">The Swedish Rate Lever</div>
            <div class="tip-body">Swedish households carry extreme floating-rate mortgage debt. When Riksbank cuts, Real Estate (SX35) responds violently upward. When they hike, SX35 drops first and hardest. Watch 2Y yield as the leading signal.</div>
        </div>
        <div class="tip-card">
            <div class="tip-title">Yield Curve as a Clock</div>
            <div class="tip-body">Inverted curve (2Y &gt; 10Y) = late cycle, favor SX20 Health Care. Steepening (10Y rising faster) = early recovery, rotate into SX50 Industrials &amp; SX30 Banks. The spread chart is your economic cycle clock.</div>
        </div>
        <div class="tip-card">
            <div class="tip-title">The Export Engine</div>
            <div class="tip-body">SX50 (Volvo, Atlas Copco, ABB) is driven by global capex and SEK weakness. A weaker krona boosts their foreign earnings. When global PMIs turn up, SX50 leads. When they roll over, SX50 falls first.</div>
        </div>
        <div class="tip-card">
            <div class="tip-title">Banks = Yield Curve Bet</div>
            <div class="tip-body">SX30 Banks (SEB, Swedbank, Handelsbanken) profit from the gap between short-term deposit rates and long-term lending rates. Steeper curve = wider net interest margins = stronger bank earnings. Flat curve squeezes them.</div>
        </div>
        <div class="tip-card">
            <div class="tip-title">Timeframe Discipline</div>
            <div class="tip-body"><strong>1W view:</strong> Noise-heavy but shows acute momentum shifts &mdash; useful for timing entries. <strong>1M view:</strong> Validates emerging trends &mdash; a confirmed 1M signal is actionable. <strong>1Y view:</strong> The macro cycle &mdash; defines which regime we are in.</div>
        </div>
        <div class="tip-card">
            <div class="tip-title">Crossover = Rotation</div>
            <div class="tip-body">When two RS lines cross, capital is physically moving between those sectors. A bullish crossover (line A crosses above line B) means institutional money is reallocating from B to A. Multi-week crossovers are the strongest signals.</div>
        </div>
        <div class="tip-card">
            <div class="tip-title">Defensive vs Cyclical</div>
            <div class="tip-body">Health Care (SX20) is the classic defensive play &mdash; it outperforms when everything else is falling. If SX20 is leading on both 1M and 1Y views, the market is in risk-off mode. Reduce cyclical exposure.</div>
        </div>
        <div class="tip-card">
            <div class="tip-title">Confirmation Checklist</div>
            <div class="tip-body">Before acting on a rotation signal, confirm across layers: (1) RS trend direction on 1M, (2) yield environment supports the thesis, (3) spread direction is consistent. Two out of three = actionable. All three = high conviction.</div>
        </div>
    </div>
</div>
<div class="guide-panel">
    <h4>How to Read This Dashboard</h4>
    <details>
        <summary>RS Line (Top Chart) &mdash; Sector Relative Strength</summary>
        <ul>
            <li><span class="signal-up">Rising RS line</span> = sector outperforming OMXSPI = capital flowing IN</li>
            <li><span class="signal-down">Falling RS line</span> = sector underperforming = capital flowing OUT</li>
            <li><strong>Crossovers</strong> between sectors = rotation signal (money moving between sectors)</li>
        </ul>
    </details>
    <details>
        <summary>Yield Chart (Middle) &mdash; Riksbank Policy Signal</summary>
        <ul>
            <li><strong>Rising yields</strong> = tightening = headwind for Real Estate (SX35), tailwind for Banks (SX30)</li>
            <li><strong>Falling yields</strong> = easing cycle = boosts rate-sensitive sectors (Real Estate, growth)</li>
            <li><strong>2Y yield</strong> = near-term Riksbank rate expectations; <strong>10Y yield</strong> = long-term growth/inflation</li>
        </ul>
    </details>
    <details>
        <summary>Yield Spread (Bottom) &mdash; Recession/Recovery Gauge</summary>
        <ul>
            <li><span class="signal-down">Negative spread</span> (inversion) = recession warning = favor Health Care (SX20)</li>
            <li><span class="signal-up">Positive widening</span> = recovery expectations = favor Industrials (SX50) &amp; Banks (SX30)</li>
            <li><strong>Compressing toward zero</strong> = late-cycle caution</li>
        </ul>
    </details>
    <details>
        <summary>The Four Sectors</summary>
        <ul>
            <li><strong>SX50 Industrials</strong> &mdash; Export-heavy (Volvo, Atlas Copco, ABB). Thrives in recovery/expansion.</li>
            <li><strong>SX35 Real Estate</strong> &mdash; Rate-sensitive (Castellum, Balder). First to recover in easing cycles.</li>
            <li><strong>SX30 Banks</strong> &mdash; Margin play (SEB, Handelsbanken, Swedbank). Benefits from higher rates.</li>
            <li><strong>SX20 Health Care</strong> &mdash; Defensive (AstraZeneca, Getinge). Outperforms during recession.</li>
        </ul>
    </details>
</div>`

	script := `
<script data-id="timeframe-logic">
const originalOptions = [];

window.addEventListener('load', function() {
    setTimeout(function() {
        var items = document.querySelectorAll('.item');
        for (var i = 0; i < items.length; i++) {
            var el = items[i];
            var chart = echarts.getInstanceByDom(el);
            if (chart) {
                originalOptions.push({
                    id: el.id,
                    option: JSON.parse(JSON.stringify(chart.getOption()))
                });
            }
        }
    }, 500);
});

function extractValue(d) {
    if (d == null) return null;
    if (typeof d === 'object' && d !== null) return d.value;
    return d;
}

function isValidNumber(v) {
    return v != null && v !== '-' && typeof v === 'number' && !isNaN(v) && isFinite(v) && v !== 0;
}

function updateTimeframe(period, btn) {
    var buttons = document.querySelectorAll('.btn');
    for (var i = 0; i < buttons.length; i++) buttons[i].classList.remove('active');
    btn.classList.add('active');

    // Switch regime panel
    var panels = document.querySelectorAll('.regime-panel');
    for (var p = 0; p < panels.length; p++) panels[p].classList.remove('active');
    var target = document.getElementById('regime-' + period);
    if (target) target.classList.add('active');

    for (var oi = 0; oi < originalOptions.length; oi++) {
        var item = originalOptions[oi];
        var el = document.getElementById(item.id);
        if (!el) continue;
        var chart = echarts.getInstanceByDom(el);
        if (!chart) continue;

        var opt = JSON.parse(JSON.stringify(item.option));
        if (!opt.xAxis || !opt.xAxis[0] || !opt.xAxis[0].data) continue;
        if (!opt.series || !opt.series.length) continue;

        var allDates = opt.xAxis[0].data;
        var totalPoints = allDates.length;

        var sliceSize = totalPoints;
        if (period === '1M') sliceSize = 22;
        if (period === '1W') sliceSize = 5;

        var startIndex = Math.max(0, totalPoints - sliceSize);
        opt.xAxis[0].data = allDates.slice(startIndex);

        for (var si = 0; si < opt.series.length; si++) {
            var serie = opt.series[si];
            var newData = serie.data.slice(startIndex);

            var isRSChart = opt.title && opt.title[0] && opt.title[0].text &&
                            opt.title[0].text.indexOf('Relative Strength') !== -1;

            if (period !== '1Y' && isRSChart) {
                var baseValue = null;
                for (var k = 0; k < newData.length; k++) {
                    var v = extractValue(newData[k]);
                    if (isValidNumber(v)) { baseValue = v; break; }
                }
                if (baseValue !== null) {
                    serie.data = newData.map(function(d) {
                        var v = extractValue(d);
                        if (!isValidNumber(v)) return { value: '-' };
                        return { value: (v / baseValue) * 100 };
                    });
                } else {
                    serie.data = newData;
                }
            } else {
                serie.data = newData;
            }
        }
        chart.setOption(opt, true);
    }
}
</script>`

	htmlContent = strings.Replace(htmlContent, "</head>", css+"</head>", 1)
	htmlContent = strings.Replace(htmlContent, "<body>", "<body>"+controls, 1)
	htmlContent = strings.Replace(htmlContent, "</body>", guide+script+"</body>", 1)

	os.WriteFile(filename, []byte(htmlContent), 0644)
}

func exportLLMSignals(data []MarketData) {
	if len(data) == 0 {
		return
	}

	lastIdx := len(data) - 1
	lastData := data[lastIdx]

	rsMap := make(map[string]float64)
	if lastData.OMXSPI > 0 {
		if lastData.SX20PI > 0 { rsMap["SX20"] = (lastData.SX20PI / lastData.OMXSPI) * 100 }
		if lastData.SX30PI > 0 { rsMap["SX30"] = (lastData.SX30PI / lastData.OMXSPI) * 100 }
		if lastData.SX35PI > 0 { rsMap["SX35"] = (lastData.SX35PI / lastData.OMXSPI) * 100 }
		if lastData.SX50PI > 0 { rsMap["SX50"] = (lastData.SX50PI / lastData.OMXSPI) * 100 }
	}

	var rankings []Ranking
	for sec, val := range rsMap {
		rankings = append(rankings, Ranking{Sector: sec, RSValue: math.Round(val*1000) / 1000})
	}
	sort.Slice(rankings, func(i, j int) bool {
		return rankings[i].RSValue > rankings[j].RSValue
	})
	for i := range rankings {
		rankings[i].Rank = i + 1
	}

	getOldRS := func(idx int, sector string) float64 {
		if idx < 0 { idx = 0 }
		d := data[idx]
		if d.OMXSPI <= 0 { return 0 }
		switch sector {
		case "SX20": return (d.SX20PI / d.OMXSPI) * 100
		case "SX30": return (d.SX30PI / d.OMXSPI) * 100
		case "SX35": return (d.SX35PI / d.OMXSPI) * 100
		case "SX50": return (d.SX50PI / d.OMXSPI) * 100
		}
		return 0
	}

	idx1W := lastIdx - 5
	idx1M := lastIdx - 22

	var momentum []Momentum
	for sec, val := range rsMap {
		rs1W := getOldRS(idx1W, sec)
		rs1M := getOldRS(idx1M, sec)

		chg1W := 0.0
		if rs1W > 0 { chg1W = (val - rs1W) / rs1W }
		chg1M := 0.0
		if rs1M > 0 { chg1M = (val - rs1M) / rs1M }

		trend := "neutral"
		if chg1M > 0.02 {
			trend = "bullish"
		} else if chg1M < -0.02 {
			trend = "bearish"
		}

		momentum = append(momentum, Momentum{
			Sector:   sec,
			Change1W: math.Round(chg1W*1000) / 1000,
			Change1M: math.Round(chg1M*1000) / 1000,
			Trend:    trend,
		})
	}

	var signals []CrossoverSignal
	if lastIdx >= 1 {
		rsMapPrev := make(map[string]float64)
		for sec := range rsMap {
			rsMapPrev[sec] = getOldRS(lastIdx-1, sec)
		}
		sectors := []string{"SX20", "SX30", "SX35", "SX50"}
		for i := 0; i < len(sectors); i++ {
			for j := i + 1; j < len(sectors); j++ {
				s1, s2 := sectors[i], sectors[j]
				if rsMapPrev[s1] < rsMapPrev[s2] && rsMap[s1] > rsMap[s2] {
					signals = append(signals, CrossoverSignal{
						Sectors:     []string{s1, s2},
						Type:        "bullish_crossover",
						Description: fmt.Sprintf("%s RS crossed above %s", s1, s2),
					})
				} else if rsMapPrev[s1] > rsMapPrev[s2] && rsMap[s1] < rsMap[s2] {
					signals = append(signals, CrossoverSignal{
						Sectors:     []string{s1, s2},
						Type:        "bearish_crossover",
						Description: fmt.Sprintf("%s RS crossed below %s", s1, s2),
					})
				}
			}
		}
	}

	out := LLMOutput{
		Metadata: Metadata{
			Date:        lastData.Date,
			Benchmark:   "OMXSPI",
			Description: "Sector rotation signals based on Relative Strength (RS) against benchmark with real Riksbank yield data.",
		},
		Rankings:         rankings,
		Momentum:         momentum,
		CrossoverSignals: signals,
		Regime:           buildRegimeSummary(data, "1M", 22),
	}

	dataBytes, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile("llm_signals.json", dataBytes, 0644)
	fmt.Println("LLM JSON Signals exported: llm_signals.json")
}

func min(a, b int) int {
	if a < b { return a }
	return b
}
