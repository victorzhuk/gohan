package evals

import "github.com/victorzhuk/gohan/std/telemetry"

// Metrics returns the metric registrations online evaluation carries.
func Metrics() []telemetry.Metric {
	return []telemetry.Metric{
		{Name: telemetry.MetricEvalScore, Labels: []string{"flow", "judge"}},
		{Name: telemetry.MetricEvalUnstable, Labels: []string{"flow", "judge"}},
	}
}
