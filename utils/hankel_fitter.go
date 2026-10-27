package utils

import (
	"bufio"
	"embed"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

//go:embed data/hankel_gupt_140_1997_j1.txt
var hankelFilterFS embed.FS

type HankelFilter struct {
	Base []float64
	J1   []float64
}

var (
	hankelFilterOnce sync.Once
	hankelFilter     HankelFilter
	hankelFilterErr  error
)

func loadHankelFilter() (HankelFilter, error) {
	hankelFilterOnce.Do(func() {
		file, err := hankelFilterFS.Open(
			"data/hankel_gupt_140_1997_j1.txt",
		)
		if err != nil {
			hankelFilterErr = err
			return
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())

			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			fields := strings.Fields(line)
			if len(fields) != 2 {
				hankelFilterErr = fmt.Errorf(
					"expected base and j1 columns, got %d",
					len(fields),
				)
				return
			}

			base, err := strconv.ParseFloat(fields[0], 64)
			if err != nil || !isPositiveFinite(base) {
				hankelFilterErr = fmt.Errorf(
					"invalid Hankel filter base: %q",
					fields[0],
				)
				return
			}

			j1, err := strconv.ParseFloat(fields[1], 64)
			if err != nil || math.IsNaN(j1) ||
				math.IsInf(j1, 0) {
				hankelFilterErr = fmt.Errorf(
					"invalid J1 coefficient: %q",
					fields[1],
				)
				return
			}

			hankelFilter.Base = append(hankelFilter.Base, base)
			hankelFilter.J1 = append(hankelFilter.J1, j1)
		}

		if err := scanner.Err(); err != nil {
			hankelFilterErr = err
			return
		}

		if len(hankelFilter.Base) != 140 {
			hankelFilterErr = fmt.Errorf(
				"expected 140 filter points, got %d",
				len(hankelFilter.Base),
			)
		}
	})

	return hankelFilter, hankelFilterErr
}
