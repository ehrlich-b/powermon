# powermon

Live power monitor for Apple Silicon Macs. Displays real-time CPU, GPU, and ANE power usage alongside battery and charger stats.

![screenshot](screenshot.svg)

## Requirements

- macOS with Apple Silicon
- `sudo` access (required by `powermetrics`)

## Install

```
curl -L https://github.com/ehrlich-b/powermon/releases/latest/download/powermon -o /usr/local/bin/powermon && chmod +x /usr/local/bin/powermon
```

## Run

```
sudo powermon
```

## Build from source

```
make
sudo ./bin/powermon
```

## What it shows

- **Silicon**: Real-time CPU/GPU/ANE power draw (1s updates via `powermetrics`)
- **Charger**: Voltage, current, and wattage when plugged in
- **Power split**: How charger power divides between system and battery charging
- **Battery**: Percentage, voltage, current, temperature, and charging status
