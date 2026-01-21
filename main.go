package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PowerData struct {
	// Live from powermetrics (1s updates)
	CPUPower     float64
	GPUPower     float64
	ANEPower     float64
	PackagePower float64
	BatteryPct   int

	// From ioreg (~30s updates, but we poll every 5s)
	ChargerWatts   int
	ChargerVoltage int
	ChargerCurrent int
	BatteryVoltage int
	BatteryAmps    int
	Temperature    int
	IsCharging     bool
	OnAC           bool

	mu sync.RWMutex
}

var data PowerData

// ANSI colors
const (
	Reset   = "\033[0m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
	Dim     = "\033[2m"
)

func colorBar(pct int, width int, color string) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	empty := width - filled
	if empty < 0 {
		empty = 0
	}
	return color + strings.Repeat("█", filled) + Reset + Dim + strings.Repeat("░", empty) + Reset
}

func splitBar(sysPct, batPct, width int) string {
	if sysPct < 0 {
		sysPct = 0
	}
	if batPct < 0 {
		batPct = 0
	}
	sysBars := sysPct * width / 100
	batBars := width - sysBars
	if sysBars < 0 {
		sysBars = 0
	}
	if batBars < 0 {
		batBars = 0
	}
	return Cyan + strings.Repeat("█", sysBars) + Reset + Yellow + strings.Repeat("█", batBars) + Reset
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visibleLen(s string) int {
	return len([]rune(ansiRe.ReplaceAllString(s, "")))
}

func line(content string) string {
	const width = 52
	visible := visibleLen(content)
	pad := width - visible
	if pad < 0 {
		pad = 0
	}
	return "║ " + content + strings.Repeat(" ", pad) + " ║"
}

// Poll ioreg for charger/battery hardware data
func pollIoreg() {
	patterns := map[string]*regexp.Regexp{
		"watts":    regexp.MustCompile(`"Watts"=(\d+)`),
		"adapterV": regexp.MustCompile(`"AdapterVoltage"=(\d+)`),
		"adapterA": regexp.MustCompile(`"Current"=(\d+)`),
		"batteryV": regexp.MustCompile(`"AppleRawBatteryVoltage" = (\d+)`),
		"batteryA": regexp.MustCompile(`"Amperage" = (-?\d+)`),
		"temp":     regexp.MustCompile(`"Temperature" = (\d+)`),
		"charging": regexp.MustCompile(`"IsCharging" = (Yes|No)`),
		"external": regexp.MustCompile(`"ExternalConnected" = (Yes|No)`),
	}

	// Only returns value if in sane range, otherwise returns (0, false)
	extractInt := func(s string, re *regexp.Regexp, min, max int) (int, bool) {
		if m := re.FindStringSubmatch(s); len(m) > 1 {
			v, err := strconv.Atoi(m[1])
			if err == nil && v >= min && v <= max {
				return v, true
			}
		}
		return 0, false
	}

	for {
		out, err := exec.Command("ioreg", "-rn", "AppleSmartBattery").Output()
		if err == nil {
			s := string(out)
			data.mu.Lock()
			if v, ok := extractInt(s, patterns["watts"], 0, 500); ok {
				data.ChargerWatts = v
			}
			if v, ok := extractInt(s, patterns["adapterV"], 0, 50000); ok {
				data.ChargerVoltage = v
			}
			if v, ok := extractInt(s, patterns["adapterA"], 0, 10000); ok {
				data.ChargerCurrent = v
			}
			if v, ok := extractInt(s, patterns["batteryV"], 5000, 25000); ok {
				data.BatteryVoltage = v
			}
			if v, ok := extractInt(s, patterns["batteryA"], -15000, 15000); ok {
				data.BatteryAmps = v
			}
			if v, ok := extractInt(s, patterns["temp"], 0, 10000); ok {
				data.Temperature = v
			}
			if m := patterns["charging"].FindStringSubmatch(s); len(m) > 1 {
				data.IsCharging = m[1] == "Yes"
			}
			if m := patterns["external"].FindStringSubmatch(s); len(m) > 1 {
				data.OnAC = m[1] == "Yes"
			}
			data.mu.Unlock()
		}
		time.Sleep(5 * time.Second)
	}
}

func main() {
	fmt.Print("\033[?25l")     // hide cursor
	fmt.Print("\033[H\033[2J") // clear

	// Start ioreg polling in background
	go pollIoreg()

	// Launch powermetrics
	cmd := exec.Command("sudo", "powermetrics",
		"--samplers", "cpu_power,gpu_power,battery",
		"-i", "1000",
		"-f", "text")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Println("Error starting powermetrics (need sudo):", err)
		return
	}

	// Handle Ctrl+C: kill powermetrics and restore cursor
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		cmd.Process.Kill()
		fmt.Print("\033[?25h\n")
		os.Exit(0)
	}()

	defer cmd.Process.Kill()

	scanner := bufio.NewScanner(stdout)

	// Regex patterns for powermetrics
	cpuPowerRe := regexp.MustCompile(`CPU Power:\s+([\d.]+)\s+mW`)
	gpuPowerRe := regexp.MustCompile(`GPU Power:\s+([\d.]+)\s+mW`)
	anePowerRe := regexp.MustCompile(`ANE Power:\s+([\d.]+)\s+mW`)
	packageRe := regexp.MustCompile(`Combined Power \(CPU \+ GPU \+ ANE\):\s+([\d.]+)\s+mW`)
	batteryPctRe := regexp.MustCompile(`percent_charge:\s+(\d+)`)

	for scanner.Scan() {
		text := scanner.Text()

		data.mu.Lock()
		if m := cpuPowerRe.FindStringSubmatch(text); m != nil {
			data.CPUPower, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := gpuPowerRe.FindStringSubmatch(text); m != nil {
			data.GPUPower, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := anePowerRe.FindStringSubmatch(text); m != nil {
			data.ANEPower, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := packageRe.FindStringSubmatch(text); m != nil {
			data.PackagePower, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := batteryPctRe.FindStringSubmatch(text); m != nil {
			data.BatteryPct, _ = strconv.Atoi(m[1])
		}
		data.mu.Unlock()

		if strings.HasPrefix(text, "***") {
			render()
		}
	}
}

func render() {
	data.mu.RLock()
	defer data.mu.RUnlock()

	fmt.Print("\033[H") // cursor home

	cpuW := data.CPUPower / 1000
	gpuW := data.GPUPower / 1000
	aneW := data.ANEPower / 1000
	siliconW := data.PackagePower / 1000

	chargerV := float64(data.ChargerVoltage) / 1000
	chargerA := float64(data.ChargerCurrent) / 1000
	batteryV := float64(data.BatteryVoltage) / 1000
	batteryA := float64(data.BatteryAmps) / 1000
	batteryW := batteryV * batteryA
	tempC := float64(data.Temperature) / 100

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println(line("       LIVE POWER MONITOR  (Ctrl+C to stop)"))
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Println(line(Magenta + "SILICON" + Reset + " (live)"))
	fmt.Println(line(fmt.Sprintf("  CPU:  %5.2f W  [%s]", cpuW, colorBar(int(cpuW*10), 20, Magenta))))
	fmt.Println(line(fmt.Sprintf("  GPU:  %5.2f W  [%s]", gpuW, colorBar(int(gpuW*10), 20, Magenta))))
	fmt.Println(line(fmt.Sprintf("  ANE:  %5.2f W  [%s]", aneW, colorBar(int(aneW*10), 20, Magenta))))
	fmt.Println(line(fmt.Sprintf("  Chip: %5.2f W", siliconW)))

	fmt.Println("╠══════════════════════════════════════════════════════╣")

	if data.OnAC {
		systemW := float64(data.ChargerWatts) - batteryW
		fmt.Println(line(Green + "CHARGER" + Reset))
		fmt.Println(line(fmt.Sprintf("  %.1fV × %.2fA = " + Green + "%dW" + Reset, chargerV, chargerA, data.ChargerWatts)))
		fmt.Println("╠══════════════════════════════════════════════════════╣")
		fmt.Println(line("POWER SPLIT (~30s refresh)"))
		fmt.Println(line(fmt.Sprintf("  → " + Cyan + "System:  %5.1f W" + Reset, systemW)))
		fmt.Println(line(fmt.Sprintf("  → " + Yellow + "Battery: %5.1f W" + Reset, batteryW)))

		// Visual split bar
		if data.ChargerWatts > 0 {
			barWidth := 40
			batteryPct := int((batteryW / float64(data.ChargerWatts)) * 100)
			if batteryPct < 0 {
				batteryPct = 0
			}
			if batteryPct > 100 {
				batteryPct = 100
			}
			systemPct := 100 - batteryPct
			fmt.Println(line(fmt.Sprintf("  [%s]", splitBar(systemPct, batteryPct, barWidth))))
			fmt.Println(line(fmt.Sprintf("   " + Cyan + "system %d%%" + Reset + "          " + Yellow + "battery %d%%" + Reset, systemPct, batteryPct)))
		}
	} else {
		drainW := -batteryW
		fmt.Println(line(Red + "ON BATTERY" + Reset))
		fmt.Println(line(fmt.Sprintf("  Drain: " + Red + "%.1f W" + Reset, drainW)))
	}

	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Println(line(Yellow + "BATTERY" + Reset))

	status := Red + "draining" + Reset
	if data.IsCharging {
		status = Green + "charging" + Reset
	} else if data.OnAC {
		status = Blue + "full/maintaining" + Reset
	}

	fmt.Println(line(fmt.Sprintf("  %d%% │ %.2fV │ %dmA │ %.1f°C", data.BatteryPct, batteryV, data.BatteryAmps, tempC)))
	fmt.Println(line(fmt.Sprintf("  %s", status)))
	fmt.Println(line(fmt.Sprintf("  [%s]", colorBar(data.BatteryPct, 44, Yellow))))

	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Println(line(time.Now().Format("15:04:05")))
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
}
