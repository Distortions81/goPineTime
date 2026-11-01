package main

import "time"

// A bounded autocorrelation estimator compares complete optical waveforms,
// rather than requiring individual peaks to cross a fixed ADC threshold.
// A 6.4-second window and two agreeing estimates are required. Constant levels,
// clipped ADC values, timing gaps and uncorrelated noise do not produce BPM.
// This remains an experimental estimate until validated with watch recordings.
type pulseDetector struct {
	dc, smooth                    int32
	last                          time.Time
	values                        [64]int16
	count, head, warmup, analysis uint8
	candidate                     uint16
}

func (p *pulseDetector) sample(raw uint16, now time.Time) uint16 {
	if raw == 0 || raw == 65535 || (!p.last.IsZero() && (now.Sub(p.last) > 5*heartSamplePeriod/2 || !now.After(p.last))) {
		*p = pulseDetector{}
		return 0
	}
	if p.last.IsZero() {
		p.dc = int32(raw) * 256
	}
	p.last = now
	p.dc += (int32(raw)*256 - p.dc) / 16
	p.smooth += (int32(raw)*256 - p.dc - p.smooth) / 2
	if p.warmup < 20 {
		p.warmup++
		return 0
	}
	// Retain four fractional ADC bits so weak signals survive the filter.
	p.values[p.head] = int16(max(-32767, min(32767, p.smooth/16)))
	p.head = (p.head + 1) & 63
	if p.count < 64 {
		p.count++
	}
	p.analysis++
	if p.count < 64 || p.analysis < 5 {
		return 0
	}
	p.analysis = 0
	bpm := p.estimate()
	previous := p.candidate
	p.candidate = bpm
	if bpm == 0 || previous == 0 || max(bpm, previous)-min(bpm, previous) > max(uint16(5), previous/10) {
		return 0
	}
	return (bpm + previous) / 2
}

//go:noinline
func (p *pulseDetector) estimate() uint16 {
	// Normalize once per window to keep covariance products bounded in int64.
	var amplitude int32
	for _, v := range p.values {
		amplitude = max(amplitude, max(int32(v), -int32(v)))
	}
	if amplitude == 0 {
		return 0
	}
	shift := uint(0)
	for amplitude > 1023 {
		amplitude >>= 1
		shift++
	}
	var scores [17]int32
	var values [64]int16
	var sx, xx int32
	for i := range values {
		values[i] = p.values[(int(p.head)+i)&63] >> shift
		if i < 48 {
			x := int32(values[i])
			sx += x
			xx += x * x
		}
	}
	// All lags use the same overlap; subtract each segment's mean so slow
	// baseline movement cannot count as periodic optical energy.
	const overlap = 48
	for lag := 2; lag <= 16; lag++ {
		var sy, yy, xy int32
		for i := 0; i < overlap; i++ {
			x := int32(values[i])
			y := int32(values[i+lag])
			sy += y
			yy += y * y
			xy += x * y
		}
		covariance := overlap*int64(xy) - int64(sx)*int64(sy)
		energy := max(overlap*int64(xx)-int64(sx)*int64(sx), overlap*int64(yy)-int64(sy)*int64(sy))
		if energy > 0 {
			scores[lag] = int32(covariance * 1024 / energy)
		}
	}
	// Prefer the first strong local maximum to avoid reporting a multiple of
	// the pulse period. Neighbor interpolation reduces 10 Hz quantization.
	for lag := 3; lag <= 15; lag++ {
		score := scores[lag]
		if score < 700 || score <= scores[lag-1] || score < scores[lag+1] {
			continue
		}
		curvature := 2*score - scores[lag-1] - scores[lag+1]
		fraction := int32(0)
		if curvature > 0 {
			fraction = (scores[lag+1] - scores[lag-1]) * 128 / curvature
		}
		period := int32(lag*256) + max(-128, min(128, fraction))
		bpm := uint16((600*256 + period/2) / period)
		if bpm >= 40 && bpm <= 200 {
			return bpm
		}
	}
	return 0
}
