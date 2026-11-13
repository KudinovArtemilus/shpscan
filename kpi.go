package main

// kpi.go — превращает один файл (один цикл прокатки) в набор показателей.

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// AllSignals включает показатели по всем сигналам файла, а не только по спискам профиля.
var AllSignals = true

// Одинаковые названия показателей повторяются в каждом цикле. Чтобы на большом архиве
// не хранить миллионы копий одной строки, каждое название хранится один раз.
var (
	internMu  sync.Mutex
	internMap = map[string]string{}
)

func intern(s string) string {
	internMu.Lock()
	defer internMu.Unlock()
	if v, ok := internMap[s]; ok {
		return v
	}
	internMap[s] = s
	return s
}

// Номер операции в имени файла: ..._OP1_...
var opInName = regexp.MustCompile(`_OP(\d+)_`)

type KPI struct {
	Key   string
	Value float64
}

type StepSpan struct {
	Step    int
	T0, Dur float64 // секунды
}

type Cycle struct {
	File     string
	Profile  *Profile
	Group    string // машина и операция, например «Пресс М10 · OP1»; норма считается внутри группы
	ID       string // год.номер.плавка из сигналов, либо имя файла
	Start    time.Time
	Shift    string  // A — день (08–20), B — ночь
	Duration float64 // длительность записи, с
	LoadT0   float64 // окно «под нагрузкой», с от старта
	LoadT1   float64
	Steps    []StepSpan
	KPIs     []KPI
	Faults   []string // аварийные биты, которые срабатывали
}

func (c *Cycle) add(key string, v float64) {
	c.KPIs = append(c.KPIs, KPI{intern(key), v})
}

// At ищет показатель, сначала проверяя место hint: у циклов одной машины
// показатели идут в одном порядке, поэтому обычно поиск не нужен.
func (c *Cycle) At(key string, hint int) (float64, bool) {
	if hint >= 0 && hint < len(c.KPIs) && c.KPIs[hint].Key == key {
		return c.KPIs[hint].Value, true
	}
	return c.Get(key)
}

// Index возвращает место показателя в списке или -1.
func (c *Cycle) Index(key string) int {
	for i, k := range c.KPIs {
		if k.Key == key {
			return i
		}
	}
	return -1
}

func (c *Cycle) Get(key string) (float64, bool) {
	for _, k := range c.KPIs {
		if k.Key == key {
			return k.Value, true
		}
	}
	return 0, false
}

// Analyze считает показатели цикла. Профиль машины выбирается по сигналам файла;
// name — имя файла, как его видит пользователь. Сигналы, которых в файле нет, молча пропускаются.
func Analyze(f *File, name string, tz time.Duration) *Cycle {
	t := f.Time()
	p := pickProfile(f)
	c := &Cycle{
		File:     name,
		Profile:  p,
		Group:    p.Name,
		Start:    f.Start,
		Duration: t[len(t)-1] - t[0],
	}
	if m := opInName.FindStringSubmatch(name); m != nil {
		c.Group += " · OP" + m[1]
	}
	c.ID = strings.TrimSuffix(c.File, filepath.Ext(c.File))
	if y, n, h := f.Analog(p.IDSignals[0]), f.Analog(p.IDSignals[1]), f.Analog(p.IDSignals[2]); y != nil && n != nil && h != nil {
		last := f.N - 1
		c.ID = fmt.Sprintf("%02.0f.%06.0f.%06.0f", y[last], n[last], h[last])
	}
	if h := f.Start.Add(tz).Hour(); h >= 8 && h < 20 {
		c.Shift = "A"
	} else {
		c.Shift = "B"
	}
	c.add("t.cycle_s", c.Duration)

	// Шаги цикла.
	if st := f.Analog(p.StepSignal); st != nil {
		from := 0
		for i := 1; i <= len(st); i++ {
			if i == len(st) || st[i] != st[from] {
				end := t[len(t)-1]
				if i < len(st) {
					end = t[i]
				}
				c.Steps = append(c.Steps, StepSpan{int(st[from]), t[from], end - t[from]})
				from = i
			}
		}
		sum := map[int]float64{}
		for _, s := range c.Steps {
			sum[s.Step] += s.Dur
		}
		steps := make([]int, 0, len(sum))
		for s := range sum {
			steps = append(steps, s)
		}
		sort.Ints(steps)
		for _, s := range steps {
			c.add(fmt.Sprintf("step.%02d_s", s), sum[s])
		}
	}

	// Окно нагрузки: от первого до последнего отсчёта, где усилие выше 10 % своего максимума.
	a, b := 0, f.N
	peak := -1 // отсчёт с максимальным усилием
	if load := f.Analog(p.LoadSignal); load != nil {
		if thr := 0.1 * maxOf(load); thr > 0 {
			first, last := -1, -1
			for i, v := range load {
				if v > thr {
					if first < 0 {
						first = i
					}
					last = i
				}
			}
			a, b = first, last+1
			for i, v := range load {
				if peak < 0 || v > load[peak] {
					peak = i
				}
			}
			c.LoadT0, c.LoadT1 = t[a], t[b-1]
			c.add("t.load_s", c.LoadT1-c.LoadT0)
		}
	}

	for _, name := range p.Forces {
		if v := f.Analog(name); v != nil {
			c.add(name+".max", maxOf(v))
			c.add(name+".mean_load", mean(v[a:b]))
		}
	}
	// Перекос лево/право: разница средних под нагрузкой в % от их среднего.
	for _, lr := range p.LeftRight {
		l, r := f.Analog(lr[1]), f.Analog(lr[2])
		if l == nil || r == nil {
			continue
		}
		ml, mr := mean(l[a:b]), mean(r[a:b])
		if avg := (ml + mr) / 2; avg != 0 {
			c.add("asym."+lr[0]+"_%", 100*(ml-mr)/avg)
		}
	}
	for _, name := range p.Peaks {
		if v := f.Analog(name); v != nil {
			c.add(name+".max", maxOf(v))
		}
	}
	for _, name := range p.Mins {
		if v := f.Analog(name); v != nil {
			c.add(name+".min", minOf(v))
		}
	}
	for _, name := range p.Ranges {
		if v := f.Analog(name); v != nil {
			c.add(name+".range", maxOf(v)-minOf(v))
		}
	}
	for _, name := range p.AbsPeaks {
		if v := f.Analog(name); v != nil {
			c.add(name+".absmax", math.Max(math.Abs(maxOf(v)), math.Abs(minOf(v))))
		}
	}
	for _, name := range p.AtLoadEnd {
		if v := f.Analog(name); v != nil {
			c.add(name+".end", v[b-1])
		}
	}

	if peak >= 0 {
		for _, name := range p.AtLoadPeak {
			if v := f.Analog(name); v != nil {
				c.add(name+".at_peak", v[peak])
			}
		}
	}

	// Слежение: задание против обратной связи (клапаны, наклон шайбы насосов).
	// Пары находятся по окончаниям имён из профиля. Среднеквадратичное рассогласование
	// растёт, когда клапан начинает «залипать» или привод не успевает за заданием.
	done := map[int]bool{}
	for i, name := range f.AnalogNames {
		for _, sf := range p.PairSuffixes {
			if done[i] || !strings.HasSuffix(name, sf[0]) {
				continue
			}
			fb := f.Analog(strings.TrimSuffix(name, sf[0]) + sf[1])
			if fb == nil || minOf(fb) == maxOf(fb) { // пары нет или датчик обратной связи не подключён
				continue
			}
			done[i] = true
			cmd := f.AnalogAt(i)
			var s float64
			for k := range cmd {
				d := cmd[k] - fb[k]
				s += d * d
			}
			c.add("track."+name+".rms", math.Sqrt(s/float64(len(cmd))))
		}
	}

	// Аварийные биты.
	for i, name := range f.BitNames {
		if !hasAnyPrefix(name, p.FaultPrefixes) {
			continue
		}
		for _, on := range f.BitAt(i) {
			if on {
				c.Faults = append(c.Faults, name)
				break
			}
		}
	}
	c.add("faults.count", float64(len(c.Faults)))

	for _, name := range p.Counters {
		if v := f.Analog(name); v != nil {
			c.add("cnt."+name, v[len(v)-1])
		}
	}
	if AllSignals {
		addAllSignals(f, c, t)
	}
	return c
}

// addAllSignals добавляет показатели по каждому сигналу файла:
//
//	all.<имя>.min / .max / .mean  — для аналоговых
//	bit.<имя>.on_s / .n           — для дискретных: сколько секунд был включён и сколько раз включался
//
// Сигнал с повторяющимся именем (резервные каналы) учитывается один раз — первый.
func addAllSignals(f *File, c *Cycle, t []float64) {
	have := map[string]bool{}
	for _, k := range c.KPIs {
		have[k.Key] = true
	}
	for i, name := range f.AnalogNames {
		if f.anIdx[name] != i {
			continue
		}
		v := f.AnalogAt(i)
		if !have[name+".min"] { // минимум и максимум могли уже попасть в показатели из профиля
			c.add("all."+name+".min", minOf(v))
		}
		if !have[name+".max"] {
			c.add("all."+name+".max", maxOf(v))
		}
		c.add("all."+name+".mean", mean(v))
	}
	dt := 0.0
	if len(t) > 1 {
		dt = (t[len(t)-1] - t[0]) / float64(len(t)-1)
	}
	for i, name := range f.BitNames {
		if f.bitIdx[name] != i {
			continue
		}
		on, rises, prev := 0, 0, false
		for k, b := range f.BitAt(i) {
			if b {
				on++
				if k > 0 && !prev {
					rises++
				}
			}
			prev = b
		}
		c.add("bit."+name+".on_s", float64(on)*dt)
		c.add("bit."+name+".n", float64(rises))
	}
}

// isAuto — показатель из набора «по всем сигналам», а не из профиля машины.
func isAuto(key string) bool {
	return strings.HasPrefix(key, "all.") || strings.HasPrefix(key, "bit.")
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func maxOf(v []float64) float64 {
	m := math.Inf(-1)
	for _, x := range v {
		if x > m {
			m = x
		}
	}
	return m
}

func minOf(v []float64) float64 {
	m := math.Inf(1)
	for _, x := range v {
		if x < m {
			m = x
		}
	}
	return m
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}
