package main

import (
	"testing"
	"time"

	"tinygo.org/x/drivers/pixel"
)

// Exclude host-side framebuffer copies from the raster benchmark. The watch
// transmits this same packed buffer using SPI DMA.
type benchmarkDisplay struct{ memoryDisplay }

func (*benchmarkDisplay) DrawBitmap(int16, int16, pixel.Image[pixel.RGB444BE]) error {
	return nil
}

var benchmarkKey frameKey

func BenchmarkFrameKey(b *testing.B) {
	for _, tc := range []struct {
		name string
		page page
	}{
		{"Clock", pageClock}, {"Stopwatch", pageStopwatch}, {"Sync", pageTimeSync},
	} {
		b.Run(tc.name, func(b *testing.B) {
			u := newWatchUI(firmwareConfirmed)
			u.page = tc.page
			now := time.Unix(0, 0)
			b.ReportAllocs()
			for b.Loop() {
				benchmarkKey = u.frameKey(now, powerStatus{Percent: 68})
			}
		})
	}
}

func BenchmarkRender(b *testing.B) {
	for _, tc := range []struct {
		name string
		page page
	}{
		{"Clock", pageClock}, {"Stopwatch", pageStopwatch}, {"Update", pageUpdate}, {"Settings", pageSettings},
	} {
		b.Run(tc.name, func(b *testing.B) {
			u := newWatchUI(firmwareConfirmed)
			u.page, u.holding, u.holdStep = tc.page, true, 12
			now := time.Unix(0, 0)
			d := &benchmarkDisplay{}
			var r frameRenderer
			draw := func(c canvas) { u.drawFrame(c, now, powerStatus{Percent: 68}) }
			if err := r.render(d, draw); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := r.render(d, draw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestHotUIPathsDoNotAllocate(t *testing.T) {
	now := time.Unix(0, 0)
	for _, p := range []page{pageClock, pageApps, pageDisplaySettings, pageStopwatch, pageCountdown, pageUpdate, pageSettings, pageMusic, pagePhone, pageWeather, pageWeatherForecast} {
		u := newWatchUI(firmwareConfirmed)
		u.page, u.holding, u.holdStep = p, true, 12
		u.timers.watch.lap = 1250 * time.Millisecond
		u.phone = samplePhone(now)
		u.weather = sampleWeather(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
		power := powerStatus{Percent: 100}
		d := &benchmarkDisplay{}
		var r frameRenderer
		draw := func(c canvas) { u.drawFrame(c, now, power) }
		if err := r.render(d, draw); err != nil {
			t.Fatal(err)
		}
		for _, light := range []bool{false, true} {
			u.lightMode = light
			if got := testing.AllocsPerRun(20, func() {
				benchmarkKey = u.frameKey(now, power)
				if err := r.render(d, draw); err != nil {
					panic(err)
				}
			}); got != 0 {
				t.Fatalf("page %d (light=%v): %g allocations per frame", p, light, got)
			}
		}
	}
	for p := pageClock; p <= pageAlert; p++ {
		u := newWatchUI(firmwareConfirmed)
		u.page = p
		u.sync.Start(now)
		if got := testing.AllocsPerRun(20, func() { benchmarkKey = u.frameKey(now, powerStatus{}) }); got != 0 {
			t.Fatalf("page %d: %g frame-key allocations", p, got)
		}
	}
}
