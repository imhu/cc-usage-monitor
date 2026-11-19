package core

// UsageTone maps utilization fraction (0..1) to the 5-level color ramp
// matching kittors/claude-usage-monitor: green -> yellow-green -> amber -> orange -> red.
type UsageTone int

const (
	ToneCalm UsageTone = iota
	ToneSteady
	ToneWarm
	ToneHigh
	ToneCritical
)

func ToneFor(fraction float64) UsageTone {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	switch {
	case fraction < 0.25:
		return ToneCalm
	case fraction < 0.50:
		return ToneSteady
	case fraction < 0.75:
		return ToneWarm
	case fraction < 0.90:
		return ToneHigh
	default:
		return ToneCritical
	}
}

// Hex returns the 0xRRGGBB sRGB value for the tone.
func (t UsageTone) Hex() uint32 {
	switch t {
	case ToneCalm:
		return 0x6CC08B
	case ToneSteady:
		return 0xC5C56A
	case ToneWarm:
		return 0xE3A24B
	case ToneHigh:
		return 0xE07A3C
	default:
		return 0xE5584F
	}
}
