package main

// batch.go — обработка папки: все файлы параллельно, затем норма, аномалии и дрейф.

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	minForNorm  = 10  // меньше циклов — норму не считаем
	minForDrift = 30  // меньше циклов — дрейф не считаем
	zLimit      = 3.5 // порог аномалии в «робастных сигмах» для показателей из профиля
	zLimitAuto  = 6.0 // порог для показателей «по всем сигналам»: их сотни, поэтому строже
)

// Stat — норма одного показателя по всем циклам.
type Stat struct {
	Key      string
	N        int
	Median   float64
	Sigma    float64 // робастная оценка разброса: 1.4826 × MAD
	DriftPct float64 // медиана последней трети против первой, %
	Drifted  bool
}

type Anomaly struct {
	Cycle *Cycle
	Key   string
	Value float64
	Z     float64
}

// Group — циклы одной машины и одной операции. Норма считается только внутри группы:
// сравнивать усилие OP1 с OP2 или пресс со станом бессмысленно.
type Group struct {
	Name      string
	Cycles    []*Cycle
	Stats     []Stat
	Anomalies []Anomaly
}

type Batch struct {
	Cycles []*Cycle
	Bad    []string // файлы, которые не удалось прочитать
	Groups []*Group
}

func findFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // недоступную папку пропускаем
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".shp") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// RunBatch читает файлы параллельно: по одной горутине на ядро процессора.
func RunBatch(files []string, tz time.Duration) *Batch {
	type result struct {
		c   *Cycle
		bad string
	}
	jobs := make(chan string)
	results := make(chan result)
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				f, err := ReadFile(path)
				if err != nil {
					results <- result{bad: fmt.Sprintf("%s: %v", path, err)}
					continue
				}
				results <- result{c: Analyze(f, filepath.Base(path), tz)}
			}
		}()
	}
	go func() {
		for _, path := range files {
			jobs <- path
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	b := &Batch{}
	done := 0
	for r := range results {
		if r.c != nil {
			b.Cycles = append(b.Cycles, r.c)
		} else {
			b.Bad = append(b.Bad, r.bad)
		}
		if done++; done%500 == 0 {
			fmt.Fprintf(os.Stderr, "  обработано %d из %d\n", done, len(files))
		}
	}
	sort.Slice(b.Cycles, func(i, j int) bool { return b.Cycles[i].Start.Before(b.Cycles[j].Start) })
	sort.Strings(b.Bad)
	b.Groups = groupCycles(b.Cycles)
	return b
}

// groupCycles раскладывает циклы (уже отсортированные по времени) по группам и считает норму в каждой.
func groupCycles(cycles []*Cycle) []*Group {
	byName := map[string]*Group{}
	var groups []*Group
	for _, c := range cycles {
		g := byName[c.Group]
		if g == nil {
			g = &Group{Name: c.Group}
			byName[c.Group] = g
			groups = append(groups, g)
		}
		g.Cycles = append(g.Cycles, c)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	for _, g := range groups {
		g.computeStats()
	}
	return groups
}

// kpiKeys возвращает все показатели в порядке первого появления.
func kpiKeys(cycles []*Cycle) []string {
	seen := map[string]bool{}
	var keys []string
	for _, c := range cycles {
		for _, k := range c.KPIs {
			if !seen[k.Key] {
				seen[k.Key] = true
				keys = append(keys, k.Key)
			}
		}
	}
	return keys
}

func (g *Group) computeStats() {
	// Собираем значения по столбцам за один проход по циклам.
	type column struct {
		vals   []float64
		owners []*Cycle
	}
	cols := map[string]*column{}
	var order []string
	for _, c := range g.Cycles {
		for _, k := range c.KPIs {
			col := cols[k.Key]
			if col == nil {
				col = &column{}
				cols[k.Key] = col
				order = append(order, k.Key)
			}
			col.vals = append(col.vals, k.Value)
			col.owners = append(col.owners, c)
		}
	}

	for _, key := range order {
		vals, owners := cols[key].vals, cols[key].owners
		st := Stat{Key: key, N: len(vals), Median: median(vals)}
		dev := make([]float64, len(vals))
		for i, v := range vals {
			dev[i] = math.Abs(v - st.Median)
		}
		st.Sigma = 1.4826 * median(dev)
		// Если больше половины значений одинаковы (сигнал обычно стоит на месте), MAD равен нулю.
		// Тогда берём среднее отклонение: редкий выход такого сигнала из покоя — как раз то, что ищем.
		if st.Sigma == 0 {
			st.Sigma = 1.2533 * mean(dev)
		}

		// Счётчики (ходы, номер заготовки) растут сами по себе — норму по ним не считаем.
		// Узнаём их по префиксу cnt. или по тому, что значение от цикла к циклу только растёт.
		if !strings.HasPrefix(key, "cnt.") && !onlyGrows(vals) && st.Sigma > 0 {
			limit := zLimit
			if isAuto(key) {
				limit = zLimitAuto
			}
			if st.N >= minForNorm {
				for i, v := range vals {
					if z := (v - st.Median) / st.Sigma; math.Abs(z) > limit {
						g.Anomalies = append(g.Anomalies, Anomaly{owners[i], key, v, z})
					}
				}
			}
			if st.N >= minForDrift {
				third := st.N / 3
				head := vals[:third]
				d := median(vals[st.N-third:]) - median(head)
				if st.Median != 0 {
					st.DriftPct = 100 * d / math.Abs(st.Median)
				}
				// Сравниваем сдвиг с разбросом первой трети: общий разброс
				// для этого не годится — дрейф сам его раздувает.
				hd := make([]float64, len(head))
				hm := median(head)
				for i, v := range head {
					hd[i] = math.Abs(v - hm)
				}
				if hs := 1.4826 * median(hd); hs > 0 {
					st.Drifted = math.Abs(d) > 3*hs
				}
			}
		}
		g.Stats = append(g.Stats, st)
	}
	sort.SliceStable(g.Anomalies, func(i, j int) bool {
		return math.Abs(g.Anomalies[i].Z) > math.Abs(g.Anomalies[j].Z)
	})
}

// onlyGrows — значения ни разу не уменьшились и при этом выросли: так ведёт себя счётчик.
func onlyGrows(v []float64) bool {
	if len(v) < minForNorm || v[len(v)-1] <= v[0] {
		return false
	}
	for i := 1; i < len(v); i++ {
		if v[i] < v[i-1] {
			return false
		}
	}
	return true
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if n := len(s); n%2 == 1 {
		return s[n/2]
	} else {
		return (s[n/2-1] + s[n/2]) / 2
	}
}

// WriteCSV сохраняет таблицу «цикл × показатель».
// Разделитель «;» и десятичная запятая — чтобы русский Excel открывал без настройки.
// В pandas: pd.read_csv("cycles.csv", sep=";", decimal=",")
func (b *Batch) WriteCSV(path string) error {
	keys := kpiKeys(b.Cycles)
	var sb strings.Builder
	sb.WriteString("\xEF\xBB\xBFfile;group;id;start_utc;shift;faults;" + strings.Join(keys, ";") + "\n")
	for _, c := range b.Cycles {
		row := []string{c.File, c.Group, c.ID, c.Start.Format("2006-01-02 15:04:05"), c.Shift, strings.Join(c.Faults, ", ")}
		byKey := make(map[string]float64, len(c.KPIs))
		for _, k := range c.KPIs {
			byKey[k.Key] = k.Value
		}
		for _, key := range keys {
			cell := ""
			if v, ok := byKey[key]; ok {
				cell = strings.Replace(fmt.Sprintf("%.6g", v), ".", ",", 1)
			}
			row = append(row, cell)
		}
		sb.WriteString(strings.Join(row, ";") + "\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
