package cvewatch

import (
	"math"
	"strings"
)

type cvss3 struct {
	Score   float64
	Network bool
}

var cvss3Weights = map[string]map[string]float64{
	"AV": {"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2},
	"AC": {"L": 0.77, "H": 0.44},
	"UI": {"N": 0.85, "R": 0.62},
	"C":  {"H": 0.56, "L": 0.22, "N": 0},
	"I":  {"H": 0.56, "L": 0.22, "N": 0},
	"A":  {"H": 0.56, "L": 0.22, "N": 0},
}

func parseCVSS3(vector string) (cvss3, bool) {
	if !strings.HasPrefix(vector, "CVSS:3.") {
		return cvss3{}, false
	}
	m := map[string]string{}
	for _, part := range strings.Split(vector, "/")[1:] {
		k, v, ok := strings.Cut(part, ":")
		if ok {
			m[k] = v
		}
	}
	w := func(metric string) (float64, bool) {
		x, ok := cvss3Weights[metric][m[metric]]
		return x, ok
	}
	av, ok1 := w("AV")
	ac, ok2 := w("AC")
	ui, ok3 := w("UI")
	c, ok4 := w("C")
	i, ok5 := w("I")
	a, ok6 := w("A")
	scope := m["S"]
	var pr float64
	switch m["PR"] {
	case "N":
		pr = 0.85
	case "L":
		pr = map[bool]float64{true: 0.68, false: 0.62}[scope == "C"]
	case "H":
		pr = map[bool]float64{true: 0.5, false: 0.27}[scope == "C"]
	default:
		return cvss3{}, false
	}
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) || (scope != "U" && scope != "C") {
		return cvss3{}, false
	}
	iss := 1 - (1-c)*(1-i)*(1-a)
	var impact float64
	if scope == "U" {
		impact = 6.42 * iss
	} else {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	}
	exploit := 8.22 * av * ac * pr * ui
	score := 0.0
	if impact > 0 {
		if scope == "U" {
			score = roundUp(math.Min(impact+exploit, 10))
		} else {
			score = roundUp(math.Min(1.08*(impact+exploit), 10))
		}
	}
	return cvss3{Score: score, Network: m["AV"] == "N"}, true
}

func roundUp(x float64) float64 {
	n := int(math.Round(x * 100000))
	if n%10000 == 0 {
		return float64(n) / 100000
	}
	return (math.Floor(float64(n)/10000) + 1) / 10
}

func severityOfScore(s float64) string {
	switch {
	case s >= 9:
		return "CRITICAL"
	case s >= 7:
		return "HIGH"
	case s >= 4:
		return "MEDIUM"
	case s > 0:
		return "LOW"
	}
	return ""
}

func cvss4Network(vector string) bool {
	return strings.HasPrefix(vector, "CVSS:4.") && strings.Contains(vector, "/AV:N/")
}
