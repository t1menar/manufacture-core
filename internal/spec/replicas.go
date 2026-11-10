package spec

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ScaleMetric is the signal an autoscaled module follows.
type ScaleMetric string

// Scale metrics.
const (
	ScaleQueueDepth ScaleMetric = "queue_depth"
	ScaleCPU        ScaleMetric = "cpu"
)

// Replicas is either a fixed count (Min == Max, Autoscale false) or an autoscaling range.
// In YAML a fixed count is written as a number: "replicas: 2".
type Replicas struct {
	Min       int
	Max       int
	Autoscale bool
	Metric    ScaleMetric
	Target    int
}

// Fixed returns a fixed replica count.
func Fixed(n int) Replicas { return Replicas{Min: n, Max: n} }

type replicasRange struct {
	Min    int         `json:"min"`
	Max    int         `json:"max"`
	Metric ScaleMetric `json:"metric,omitempty"`
	Target int         `json:"target,omitempty"`
}

// UnmarshalJSON accepts a number or a {min, max, metric, target} object.
func (r *Replicas) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] != '{' {
		var n int
		if err := json.Unmarshal(b, &n); err != nil {
			return errors.New("replicas must be an integer or an object with min and max")
		}
		*r = Fixed(n)
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var v replicasRange
	if err := dec.Decode(&v); err != nil {
		return err
	}
	*r = Replicas{Min: v.Min, Max: v.Max, Autoscale: true, Metric: v.Metric, Target: v.Target}
	if r.Metric == "" {
		r.Metric = ScaleQueueDepth
	}
	return nil
}

// MarshalJSON writes the form UnmarshalJSON accepts.
func (r Replicas) MarshalJSON() ([]byte, error) {
	if !r.Autoscale {
		return json.Marshal(r.Min)
	}
	return json.Marshal(replicasRange{Min: r.Min, Max: r.Max, Metric: r.Metric, Target: r.Target})
}
