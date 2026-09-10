package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
)

const junitDir = "build/reports/junit"

// TestCase is one JUnit test case.
type TestCase struct {
	File      string  `json:"file"`
	Suite     string  `json:"suite"`
	Classname string  `json:"classname"`
	Name      string  `json:"name"`
	Time      float64 `json:"time"`
	Status    string  `json:"status"` // passed, failed, skipped
}

type junitCase struct {
	Name      string    `xml:"name,attr"`
	Classname string    `xml:"classname,attr"`
	Time      float64   `xml:"time,attr"`
	Failure   *struct{} `xml:"failure"`
	Error     *struct{} `xml:"error"`
	Skipped   *struct{} `xml:"skipped"`
}

type junitSuite struct {
	Name   string       `xml:"name,attr"`
	Cases  []junitCase  `xml:"testcase"`
	Suites []junitSuite `xml:"testsuite"`
}

type junitRoot struct {
	XMLName xml.Name
	Name    string       `xml:"name,attr"`
	Cases   []junitCase  `xml:"testcase"`
	Suites  []junitSuite `xml:"testsuite"`
}

func readJUnit(dir string) ([]TestCase, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.xml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []TestCase
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var root junitRoot
		if err := xml.Unmarshal(b, &root); err != nil {
			return nil, err
		}
		var walk func(suite string, cases []junitCase, suites []junitSuite)
		walk = func(suite string, cases []junitCase, suites []junitSuite) {
			for _, c := range cases {
				st := "passed"
				switch {
				case c.Failure != nil || c.Error != nil:
					st = "failed"
				case c.Skipped != nil:
					st = "skipped"
				}
				out = append(out, TestCase{File: filepath.Base(f), Suite: suite, Classname: c.Classname, Name: c.Name, Time: c.Time, Status: st})
			}
			for _, s := range suites {
				walk(s.Name, s.Cases, s.Suites)
			}
		}
		walk(root.Name, root.Cases, root.Suites)
	}
	return out, nil
}
