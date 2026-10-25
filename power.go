package main

type chargeState uint8

const (
	chargeDischarging chargeState = iota
	chargeCharging
	chargeExternalPower
)

type powerStatus struct {
	Percent    uint8
	Millivolts uint16
	State      chargeState
}

// Both charger signals are active-low. Loss of external power wins over a
// charging indication that has not settled yet during unplugging.
func chargerState(powerPresent, charging bool) chargeState {
	if !powerPresent {
		return chargeDischarging
	}
	if charging {
		return chargeCharging
	}
	return chargeExternalPower
}

type batteryPoint struct {
	millivolts uint16
	percent    uint8
}

// PineTime measurements from three watches, fitted by Finlay Davidson and
// adopted in InfiniTime commit 8b0d888952bb3cfbf587ab20d5096f2e578a6107.
// Dataset: https://gist.github.com/FintasticMan/72d3aa1fc5c7cb9739f45b81197836c7
// This voltage estimate is not calibrated to an individual battery or load.
var batteryCurve = [...]batteryPoint{
	{millivolts: 3500, percent: 0},
	{millivolts: 3616, percent: 3},
	{millivolts: 3723, percent: 22},
	{millivolts: 3776, percent: 48},
	{millivolts: 3979, percent: 79},
	{millivolts: 4180, percent: 100},
}

func adcToBatteryMillivolts(raw uint16) uint16 {
	// TinyGo normalizes ADC readings to 16 bits. PineTime's two equal 1 MOhm
	// resistors divide the battery voltage by two, and the ADC uses a 3 V
	// reference, giving a 0-6 V input range after compensating for the divider.
	return uint16(uint32(raw) * 6000 / 65535)
}

func estimateBatteryPercent(millivolts uint16) uint8 {
	if millivolts <= batteryCurve[0].millivolts {
		return batteryCurve[0].percent
	}

	for i := 1; i < len(batteryCurve); i++ {
		upper := batteryCurve[i]
		if millivolts <= upper.millivolts {
			lower := batteryCurve[i-1]
			voltageRange := uint32(upper.millivolts - lower.millivolts)
			percentRange := uint32(upper.percent - lower.percent)
			progress := uint32(millivolts - lower.millivolts)
			return lower.percent + uint8(progress*percentRange/voltageRange)
		}
	}

	return 100
}

func formatPowerStatus(status powerStatus) string {
	suffix := ""
	switch status.State {
	case chargeCharging:
		suffix = " CHG"
	case chargeExternalPower:
		suffix = " PWR"
	}
	return decimal(int(status.Percent)) + "%" + suffix
}
