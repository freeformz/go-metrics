package librato

// TODO: use map[string]interface{} and constants for keys for everything to
// avoid the omitempty/0 problem; remove dependency on samuel/go-librato

// TODO WIP status: test that the resulting JSON is actually correct..

import (
	"fmt"
	"github.com/rcrowley/go-metrics"
	"log"
	"math"
	"time"
)

type LibratoReporter struct {
	Email, Token string
	Source       string
	Interval     time.Duration
	Registry     metrics.Registry
	Percentiles  []float64 // percentiles to report on histogram metrics
}

func Librato(r metrics.Registry, d time.Duration, e string, t string, s string, p []float64) {
	reporter := &LibratoReporter{e, t, s, d, r, p}
	reporter.Run()
}

func (self *LibratoReporter) Run() {
	ticker := time.Tick(self.Interval)
	metricsApi := &LibratoClient{self.Email, self.Token}
	for now := range ticker {
		var metrics Batch
		var err error
		if metrics, err = self.BuildRequest(now, self.Registry); err != nil {
			log.Printf("ERROR constructing librato request body %s", err)
		}
		if err := metricsApi.PostMetrics(metrics); err != nil {
			log.Printf("ERROR sending metrics to librato %s", err)
		}
	}
}

// calculate sum of squares from data provided by metrics.Histogram
// see http://en.wikipedia.org/wiki/Standard_deviation#Rapid_calculation_methods
func sumSquares(m metrics.Histogram) float64 {
	count := float64(m.Count())
	sum := m.Mean() * float64(m.Count())
	sumSquared := math.Pow(float64(sum), 2)
	sumSquares := math.Pow(count*m.StdDev(), 2) + sumSquared/float64(m.Count())
	if math.IsNaN(sumSquares) {
		return 0.0
	}
	return sumSquared
}
func sumSquaresTimer(m metrics.Timer) float64 {
	count := float64(m.Count())
	sum := m.Mean() * float64(m.Count())
	sumSquared := math.Pow(float64(sum), 2)
	sumSquares := math.Pow(count*m.StdDev(), 2) + sumSquared/float64(m.Count())
	if math.IsNaN(sumSquares) {
		return 0.0
	}
	return sumSquares
}

func (self *LibratoReporter) BuildRequest(now time.Time, r metrics.Registry) (snapshot Batch, err error) {
	snapshot = Batch{
		MeasureTime: now.Unix(),
		Source:      self.Source,
	}
	snapshot.MeasureTime = now.Unix()
	snapshot.Gauges = make([]Measurement, 0)
	snapshot.Counters = make([]Measurement, 0)
	histogramGaugeCount := 1 + len(self.Percentiles)
	r.Each(func(name string, metric interface{}) {
		measurement := Measurement{}
		measurement[Period] = self.Interval.Seconds()
		switch m := metric.(type) {
		case metrics.Counter:
			measurement[Name] = fmt.Sprintf("%s.%s", name, "count")
			measurement[Value] = float64(m.Count())
			snapshot.Counters = append(snapshot.Counters, measurement)
		case metrics.Gauge:
			measurement[Name] = name
			measurement[Value] = float64(m.Value())
			snapshot.Gauges = append(snapshot.Gauges, measurement)
		case metrics.Histogram:
			if m.Count() > 0 {
				gauges := make([]Measurement, histogramGaugeCount, histogramGaugeCount)
				measurement[Name] = fmt.Sprintf("%s.%s", name, "hist")
				measurement[Count] = uint64(m.Count())
				measurement[Sum] = m.Mean() * float64(m.Count())
				measurement[Max] = float64(m.Max())
				measurement[Min] = float64(m.Min())
				measurement[SumSquares] = sumSquares(m)
				gauges[0] = measurement
				for i, p := range self.Percentiles {
					pMeasurement := Measurement{}
					pMeasurement[Name] = fmt.Sprintf("%s.%.2f", measurement[Name], p)
					pMeasurement[Value] = m.Percentile(p)
					pMeasurement[Period] = measurement[Period]
					gauges[i+1] = pMeasurement
				}
				snapshot.Gauges = append(snapshot.Gauges, gauges...)
			}
		case metrics.Meter:
			measurement[Name] = name
			measurement[Value] = float64(m.Count())
			snapshot.Counters = append(snapshot.Counters, measurement)
			snapshot.Gauges = append(snapshot.Gauges,
				Measurement{
					Name:   fmt.Sprintf("%s.%s", name, "1min"),
					Value:  m.Rate1(),
					Period: int64(self.Interval.Seconds()),
				},
				Measurement{
					Name:   fmt.Sprintf("%s.%s", name, "5min"),
					Value:  m.Rate5(),
					Period: int64(self.Interval.Seconds()),
				},
				Measurement{
					Name:   fmt.Sprintf("%s.%s", name, "15min"),
					Value:  m.Rate15(),
					Period: int64(self.Interval.Seconds()),
				},
			)
		case metrics.Timer:
			if m.Count() > 0 {
				libratoName := fmt.Sprintf("%s.%s", name, "timer.mean")
				gauges := make([]Measurement, histogramGaugeCount, histogramGaugeCount)
				gauges[0] = Measurement{
					Name:       libratoName,
					Count:      uint64(m.Count()),
					Sum:        m.Mean() * float64(m.Count()),
					Max:        float64(m.Max()),
					Min:        float64(m.Min()),
					SumSquares: sumSquaresTimer(m),
					Period:     int64(self.Interval.Seconds()),
					Attributes: map[string]interface{}{
						DisplayTransform:  "x/1000000",
						DisplayUnitsLong:  "milliseconds",
						DisplayUnitsShort: "ms",
					},
				}
				for i, p := range self.Percentiles {
					gauges[i+1] = Measurement{
						Name:   fmt.Sprintf("%s.timer.%2.0f", name, p*100),
						Value:  m.Percentile(p),
						Period: int64(self.Interval.Seconds()),
						Attributes: map[string]interface{}{
							DisplayTransform:  "x/1000000",
							DisplayUnitsLong:  "milliseconds",
							DisplayUnitsShort: "ms",
						},
					}
				}
				snapshot.Gauges = append(snapshot.Gauges, gauges...)
				snapshot.Gauges = append(snapshot.Gauges,
					Measurement{
						Name:   fmt.Sprintf("%s.%s", name, "rate.1min"),
						Value:  m.Rate1(),
						Period: int64(self.Interval.Seconds()),
						Attributes: map[string]interface{}{
							DisplayUnitsLong:  "occurences",
							DisplayUnitsShort: "occ",
							DisplayMin:        "0",
						},
					},
					Measurement{
						Name:   fmt.Sprintf("%s.%s", name, "rate.5min"),
						Value:  m.Rate5(),
						Period: int64(self.Interval.Seconds()),
						Attributes: map[string]interface{}{
							DisplayUnitsLong:  "occurences",
							DisplayUnitsShort: "occ",
							DisplayMin:        "0",
						},
					},
					Measurement{
						Name:   fmt.Sprintf("%s.%s", name, "rate.15min"),
						Value:  m.Rate15(),
						Period: int64(self.Interval.Seconds()),
						Attributes: map[string]interface{}{
							DisplayUnitsLong:  "occurences",
							DisplayUnitsShort: "occ",
							DisplayMin:        "0",
						},
					},
				)
			}
		}
	})
	return
}
