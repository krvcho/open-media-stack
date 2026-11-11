package main

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func BenchmarkParseCorpus(b *testing.B) {
	f, err := os.Open("testdata/corpus.jsonl")
	if err != nil {
		b.Skip("no parser corpus in this checkout")
	}
	defer f.Close()
	var titles []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var row struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(sc.Bytes(), &row)
		titles = append(titles, row.Title)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, t := range titles {
			defaultVocab.parse(t)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N*len(titles)), "us/title")
}
