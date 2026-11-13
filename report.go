package main

// report.go — HTML-отчёты. Графики рисуются как SVG прямо в файле:
// отчёт открывается в любом браузере без интернета и без библиотек.

import (
	"fmt"
	"html"
	"math"
	"os"
	"strings"
	"time"
)

const css = `<style>
body{font-family:Segoe UI,Arial,sans-serif;background:#e4e4e4;color:#1c1c1c;margin:0;padding:16px 24px}
h1{font-size:20px;margin:0 0 4px} h2{font-size:15px;margin:22px 0 8px}
.sub{color:#555;font-size:13px;margin-bottom:12px}
.box{background:#f1f1f1;border:1px solid #bdbdbd;padding:10px 12px;margin-bottom:12px;overflow-x:auto}
.tiles{display:flex;flex-wrap:wrap;gap:8px}
.tile{background:#f1f1f1;border:1px solid #bdbdbd;padding:6px 12px;min-width:120px}
.tile b{display:block;font-size:18px} .tile span{font-size:12px;color:#555}
.bad{color:#b00000;font-weight:600} .tile.bad{border-color:#b00000}
table{border-collapse:collapse;font-size:13px}
th,td{border:1px solid #c4c4c4;padding:3px 8px;text-align:right;white-space:nowrap}
th{background:#d6d6d6} td:first-child,th:first-child{text-align:left}
.legend{font-size:12px;margin-top:4px} .legend span{margin-right:14px;white-space:nowrap}
.legend i{display:inline-block;width:14px;height:3px;vertical-align:middle;margin-right:4px}
svg{display:block;max-width:100%;height:auto} svg text{font-size:11px;fill:#444}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(420px,1fr));gap:10px}
</style>`

// Приглушённая палитра: цвет нужен только чтобы различать линии, красный оставлен для отклонений.
var palette = []string{"#1f4e79", "#5b8a3a", "#8a5a00", "#6b4c9a", "#2a8a8a", "#7a7a7a", "#a0527a"}

func esc(s string) string { return html.EscapeString(s) }

func num(v float64) string {
	switch a := math.Abs(v); {
	case a >= 1000:
		return fmt.Sprintf("%.0f", v)
	case a >= 100:
		return fmt.Sprintf("%.1f", v)
	case a >= 1:
		return fmt.Sprintf("%.2f", v)
	default:
		return fmt.Sprintf("%.4f", v)
	}
}

// niceStep подбирает «круглый» шаг сетки (1, 2, 5 × 10ⁿ).
func niceStep(span float64, ticks int) float64 {
	if span <= 0 {
		return 1
	}
	raw := span / float64(ticks)
	p := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5, 10} {
		if raw <= m*p {
			return m * p
		}
	}
	return 10 * p
}

type series struct {
	Name string
	Y    []float64
}

// lineChart рисует сигналы во времени; вертикальные линии — смена шага цикла.
func lineChart(sb *strings.Builder, t []float64, ss []series, steps []StepSpan) {
	const W, H, L, R, T, B = 1100.0, 280.0, 56.0, 10.0, 18.0, 24.0
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range ss {
		lo, hi = math.Min(lo, minOf(s.Y)), math.Max(hi, maxOf(s.Y))
	}
	if hi == lo {
		hi, lo = hi+1, lo-1
	}
	pad := (hi - lo) * 0.05
	lo, hi = lo-pad, hi+pad
	t0, t1 := t[0], t[len(t)-1]
	x := func(v float64) float64 { return L + (v-t0)/(t1-t0)*(W-L-R) }
	y := func(v float64) float64 { return T + (hi-v)/(hi-lo)*(H-T-B) }

	fmt.Fprintf(sb, `<svg viewBox="0 0 %.0f %.0f" width="%.0f"><rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="#fafafa" stroke="#aaa"/>`,
		W, H, W, L, T, W-L-R, H-T-B)
	ys := niceStep(hi-lo, 5)
	for v := math.Ceil(lo/ys) * ys; v <= hi; v += ys {
		fmt.Fprintf(sb, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" stroke="#ddd"/><text x="%.0f" y="%.1f" text-anchor="end">%s</text>`,
			L, W-R, y(v), y(v), L-5, y(v)+4, num(v))
	}
	xs := niceStep(t1-t0, 12)
	for v := math.Ceil(t0/xs) * xs; v <= t1; v += xs {
		fmt.Fprintf(sb, `<text x="%.1f" y="%.0f" text-anchor="middle">%g с</text>`, x(v), H-8, v)
	}
	for _, s := range steps {
		fmt.Fprintf(sb, `<line x1="%.1f" x2="%.1f" y1="%.0f" y2="%.0f" stroke="#999" stroke-dasharray="3 3"/>`, x(s.T0), x(s.T0), T, H-B)
		if s.Dur/(t1-t0)*(W-L-R) > 22 { // подпись только там, где она помещается
			fmt.Fprintf(sb, `<text x="%.1f" y="%.0f" text-anchor="middle">ш.%d</text>`, x(s.T0+s.Dur/2), T-5, s.Step)
		}
	}
	stride := len(t)/3000 + 1 // на длинных записях рисуем не каждую точку
	for i, s := range ss {
		sb.WriteString(`<polyline fill="none" stroke-width="1.3" stroke="` + palette[i%len(palette)] + `" points="`)
		for k := 0; k < len(t); k += stride {
			fmt.Fprintf(sb, "%.1f,%.1f ", x(t[k]), y(s.Y[k]))
		}
		sb.WriteString(`"/>`)
	}
	sb.WriteString(`</svg><div class="legend">`)
	for i, s := range ss {
		fmt.Fprintf(sb, `<span><i style="background:%s"></i>%s (мин %s, макс %s)</span>`,
			palette[i%len(palette)], esc(s.Name), num(minOf(s.Y)), num(maxOf(s.Y)))
	}
	sb.WriteString(`</div>`)
}

// WriteCycleReport — отчёт по одному файлу, сохранённый на диск.
func WriteCycleReport(path string, f *File, c *Cycle, tz time.Duration) error {
	return os.WriteFile(path, []byte(CycleReportHTML(f, c, tz)), 0o644)
}

// CycleReportHTML собирает тот же отчёт в строку — её отдаёт и режим просмотра в браузере.
func CycleReportHTML(f *File, c *Cycle, tz time.Duration) string {
	var sb strings.Builder
	p := c.Profile
	t := f.Time()
	sb.WriteString(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>` + esc(c.ID) + `</title>` + css)
	fmt.Fprintf(&sb, `<h1>%s — цикл %s</h1><div class="sub">Файл %s · старт %s · смена %s · %d отсчётов, %d аналоговых и %d дискретных сигналов</div>`,
		esc(c.Group), esc(c.ID), esc(c.File), c.Start.Add(tz).Format("02.01.2006 15:04:05"), c.Shift, f.N, len(f.AnalogNames), len(f.BitNames))

	sb.WriteString(`<div class="tiles">`)
	tile := func(label, value string, bad bool) {
		cls := "tile"
		if bad {
			cls += " bad"
		}
		fmt.Fprintf(&sb, `<div class="%s"><b>%s</b><span>%s</span></div>`, cls, value, label)
	}
	tile("длительность записи, с", num(c.Duration), false)
	if v, ok := c.Get("t.load_s"); ok {
		tile(fmt.Sprintf("под нагрузкой, с (%.1f–%.1f)", c.LoadT0, c.LoadT1), num(v), false)
	}
	for _, key := range p.Tiles {
		if v, ok := c.Get(key); ok {
			tile(esc(key), num(v), false)
		}
	}
	tile("аварийных битов", fmt.Sprint(len(c.Faults)), len(c.Faults) > 0)
	sb.WriteString(`</div>`)
	if len(c.Faults) > 0 {
		sb.WriteString(`<p class="bad">Срабатывали: ` + esc(strings.Join(c.Faults, ", ")) + `</p>`)
	}

	for _, g := range p.Charts {
		var ss []series
		for _, name := range g.Signals {
			if v := f.Analog(name); v != nil {
				ss = append(ss, series{name, v})
			}
		}
		if len(ss) == 0 {
			continue
		}
		sb.WriteString(`<h2>` + esc(g.Title) + `</h2><div class="box">`)
		lineChart(&sb, t, ss, c.Steps)
		sb.WriteString(`</div>`)
	}

	sb.WriteString(`<h2>Шаги цикла</h2><div class="box"><table><tr><th>Шаг</th><th>Начало, с</th><th>Длительность, с</th></tr>`)
	for _, s := range c.Steps {
		fmt.Fprintf(&sb, `<tr><td>%d</td><td>%.2f</td><td>%.2f</td></tr>`, s.Step, s.T0, s.Dur)
	}
	sb.WriteString(`</table></div>`)
	if len(c.Steps) == 0 {
		sb.WriteString(`<p class="sub">Сигнала с номером шага в файле нет.</p>`)
	}
	sb.WriteString(`<h2>Показатели цикла</h2><div class="box"><table><tr><th>Показатель</th><th>Значение</th></tr>`)
	for _, k := range c.KPIs {
		if !isAuto(k.Key) {
			fmt.Fprintf(&sb, `<tr><td>%s</td><td>%s</td></tr>`, esc(k.Key), num(k.Value))
		}
	}
	sb.WriteString(`</table></div>`)

	if AllSignals {
		// Минимум и максимум сигнала лежат либо в показателях профиля, либо в наборе «по всем сигналам».
		pick := func(name, what string) string {
			if v, ok := c.Get("all." + name + "." + what); ok {
				return num(v)
			}
			if v, ok := c.Get(name + "." + what); ok {
				return num(v)
			}
			return ""
		}
		still := 0
		sb.WriteString(`<h2>Все аналоговые сигналы</h2><div class="box"><table><tr><th>Сигнал</th><th>Мин</th><th>Макс</th><th>Среднее</th></tr>`)
		for i, name := range f.AnalogNames {
			if f.anIdx[name] != i {
				continue
			}
			if pick(name, "min") == pick(name, "max") {
				still++
				continue
			}
			fmt.Fprintf(&sb, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`, esc(name), pick(name, "min"), pick(name, "max"), pick(name, "mean"))
		}
		fmt.Fprintf(&sb, `</table><p class="sub">Не показаны %d сигналов, которые за запись не менялись.</p></div>`, still)

		sb.WriteString(`<h2>Дискретные сигналы</h2><div class="box"><table><tr><th>Сигнал</th><th>Включён, с</th><th>Включений</th></tr>`)
		quiet := 0
		for i, name := range f.BitNames {
			if f.bitIdx[name] != i {
				continue
			}
			on, _ := c.Get("bit." + name + ".on_s")
			n, _ := c.Get("bit." + name + ".n")
			if on == 0 {
				quiet++
				continue
			}
			fmt.Fprintf(&sb, `<tr><td>%s</td><td>%.2f</td><td>%.0f</td></tr>`, esc(name), on, n)
		}
		fmt.Fprintf(&sb, `</table><p class="sub">Не показаны %d сигналов, которые ни разу не включались.</p></div>`, quiet)
	}
	sb.WriteString(`</html>`)
	return sb.String()
}

// trendChart — один показатель по всем циклам: медиана, коридор нормы, аномалии красным.
func trendChart(sb *strings.Builder, b *Group, st Stat) {
	const W, H, L, R, T, B = 420.0, 150.0, 50.0, 8.0, 8.0, 18.0
	var vals []float64
	var ids []string
	hint := b.Cycles[0].Index(st.Key)
	for _, c := range b.Cycles {
		if v, ok := c.At(st.Key, hint); ok {
			vals = append(vals, v)
			ids = append(ids, c.ID)
		}
	}
	limit := zLimit
	if isAuto(st.Key) {
		limit = zLimitAuto
	}
	lo, hi := minOf(vals), maxOf(vals)
	bandLo, bandHi := st.Median-limit*st.Sigma, st.Median+limit*st.Sigma
	lo, hi = math.Min(lo, bandLo), math.Max(hi, bandHi)
	if hi == lo {
		hi, lo = hi+1, lo-1
	}
	pad := (hi - lo) * 0.06
	lo, hi = lo-pad, hi+pad
	n := float64(len(vals) - 1)
	if n < 1 {
		n = 1
	}
	x := func(i int) float64 { return L + float64(i)/n*(W-L-R) }
	y := func(v float64) float64 { return T + (hi-v)/(hi-lo)*(H-T-B) }

	fmt.Fprintf(sb, `<svg viewBox="0 0 %.0f %.0f"><rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="#fafafa" stroke="#aaa"/>`, W, H, L, T, W-L-R, H-T-B)
	if st.Sigma > 0 {
		fmt.Fprintf(sb, `<rect x="%.0f" y="%.1f" width="%.0f" height="%.1f" fill="#dcdcdc"/>`, L, y(bandHi), W-L-R, y(bandLo)-y(bandHi))
	}
	fmt.Fprintf(sb, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" stroke="#666"/>`, L, W-R, y(st.Median), y(st.Median))
	for _, v := range []float64{hi - pad, st.Median, lo + pad} {
		fmt.Fprintf(sb, `<text x="%.0f" y="%.1f" text-anchor="end">%s</text>`, L-4, y(v)+4, num(v))
	}
	fmt.Fprintf(sb, `<text x="%.0f" y="%.0f">цикл 1</text><text x="%.0f" y="%.0f" text-anchor="end">цикл %d</text>`, L, H-4, W-R, H-4, len(vals))
	sb.WriteString(`<polyline fill="none" stroke="#1f4e79" stroke-width="1" points="`)
	stride := len(vals)/1500 + 1 // на тысячах циклов линию прореживаем, аномалии ниже рисуем все
	for i := 0; i < len(vals); i += stride {
		fmt.Fprintf(sb, "%.1f,%.1f ", x(i), y(vals[i]))
	}
	sb.WriteString(`"/>`)
	if st.Sigma > 0 && st.N >= minForNorm {
		for i, v := range vals {
			if math.Abs(v-st.Median)/st.Sigma > limit {
				fmt.Fprintf(sb, `<circle cx="%.1f" cy="%.1f" r="3" fill="#b00000"><title>%s: %s</title></circle>`, x(i), y(v), esc(ids[i]), num(v))
			}
		}
	}
	sb.WriteString(`</svg>`)
}

// WriteBatchReport — сводный отчёт по папке: общая шапка и раздел на каждую группу (машина + операция).
func WriteBatchReport(path string, b *Batch, tz time.Duration) error {
	var sb strings.Builder
	sb.WriteString(`<!doctype html><html lang="ru"><meta charset="utf-8"><title>Сводка по циклам</title>` + css)
	first, last := b.Cycles[0], b.Cycles[len(b.Cycles)-1]
	fmt.Fprintf(&sb, `<h1>Сводка по %d циклам</h1><div class="sub">%s … %s</div>`, len(b.Cycles),
		first.Start.Add(tz).Format("02.01.2006 15:04"), last.Start.Add(tz).Format("02.01.2006 15:04"))

	bad := map[*Cycle]bool{}
	withFaults, drifted := 0, 0
	for _, g := range b.Groups {
		for _, a := range g.Anomalies {
			bad[a.Cycle] = true
		}
		for _, st := range g.Stats {
			if st.Drifted {
				drifted++
			}
		}
	}
	for _, c := range b.Cycles {
		if len(c.Faults) > 0 {
			withFaults++
		}
	}
	red := map[bool]string{true: " bad"}
	sb.WriteString(`<div class="tiles">`)
	fmt.Fprintf(&sb, `<div class="tile"><b>%d</b><span>циклов</span></div>`, len(b.Cycles))
	fmt.Fprintf(&sb, `<div class="tile%s"><b>%d</b><span>циклов с отклонениями</span></div>`, red[len(bad) > 0], len(bad))
	fmt.Fprintf(&sb, `<div class="tile%s"><b>%d</b><span>циклов с аварийными битами</span></div>`, red[withFaults > 0], withFaults)
	fmt.Fprintf(&sb, `<div class="tile%s"><b>%d</b><span>показателей с дрейфом</span></div>`, red[drifted > 0], drifted)
	fmt.Fprintf(&sb, `<div class="tile"><b>%d</b><span>нечитаемых файлов</span></div></div>`, len(b.Bad))
	sb.WriteString(`<div class="sub" style="margin-top:10px">Норма считается отдельно для каждой машины и операции. Серая полоса на графиках — коридор нормы, красные точки — выход за него. Коридор — медиана ± 3,5σ для основных показателей и ± 6σ для показателей по всем сигналам (all.… и bit.…).</div>`)

	for _, g := range b.Groups {
		fmt.Fprintf(&sb, `<h1 style="margin-top:28px">%s — %d циклов</h1>`, esc(g.Name), len(g.Cycles))
		if len(g.Cycles) < minForNorm {
			fmt.Fprintf(&sb, `<p class="sub">Циклов меньше %d — норма и аномалии не считаются, показаны только значения.</p>`, minForNorm)
		} else if len(g.Cycles) < minForDrift {
			fmt.Fprintf(&sb, `<p class="sub">Циклов меньше %d — дрейф не считается.</p>`, minForDrift)
		}

		if len(g.Anomalies) > 0 {
			sb.WriteString(`<h2>Отклонения от нормы</h2><div class="box"><table><tr><th>Цикл</th><th>Старт</th><th>Показатель</th><th>Значение</th><th>Норма (медиана)</th><th>Отклонение, σ</th></tr>`)
			stat := map[string]Stat{}
			for _, st := range g.Stats {
				stat[st.Key] = st
			}
			for i, a := range g.Anomalies {
				if i == 300 {
					fmt.Fprintf(&sb, `<tr><td colspan="6">… ещё %d, полный список считается по cycles.csv</td></tr>`, len(g.Anomalies)-300)
					break
				}
				fmt.Fprintf(&sb, `<tr><td>%s</td><td>%s</td><td>%s</td><td class="bad">%s</td><td>%s</td><td>%+.1f</td></tr>`,
					esc(a.Cycle.ID), a.Cycle.Start.Add(tz).Format("02.01 15:04:05"), esc(a.Key), num(a.Value), num(stat[a.Key].Median), a.Z)
			}
			sb.WriteString(`</table></div>`)
		}

		hasDrift := false
		for _, st := range g.Stats {
			if st.Drifted {
				if !hasDrift {
					hasDrift = true
					sb.WriteString(`<h2>Дрейф: последняя треть циклов против первой</h2><div class="box"><table><tr><th>Показатель</th><th>Медиана</th><th>Изменение, %</th></tr>`)
				}
				fmt.Fprintf(&sb, `<tr><td>%s</td><td>%s</td><td class="bad">%+.1f</td></tr>`, esc(st.Key), num(st.Median), st.DriftPct)
			}
		}
		if hasDrift {
			sb.WriteString(`</table></div>`)
		}

		// Графики: все основные показатели, а из сотен показателей по всем сигналам — только те,
		// где есть отклонение или дрейф (не больше maxAutoCharts, остальное — в cycles.csv).
		const maxAutoCharts = 80
		flagged := map[string]bool{}
		for _, a := range g.Anomalies {
			flagged[a.Key] = true
		}
		autoShown, autoSkipped := 0, 0
		sb.WriteString(`<h2>Показатели по циклам</h2><div class="grid">`)
		for _, st := range g.Stats {
			if st.N < 2 || strings.HasPrefix(st.Key, "step.") && st.Sigma == 0 {
				continue
			}
			if isAuto(st.Key) {
				if !flagged[st.Key] && !st.Drifted {
					continue
				}
				if autoShown == maxAutoCharts {
					autoSkipped++
					continue
				}
				autoShown++
			}
			title := esc(st.Key)
			if st.Drifted {
				title += fmt.Sprintf(` <span class="bad">дрейф %+.1f %%</span>`, st.DriftPct)
			}
			sb.WriteString(`<div class="box"><div style="font-size:13px">` + title + `</div>`)
			trendChart(&sb, g, st)
			sb.WriteString(`</div>`)
		}
		sb.WriteString(`</div>`)
		if autoSkipped > 0 {
			fmt.Fprintf(&sb, `<p class="sub">Ещё %d показателей по сигналам с отклонениями не показаны графиком — они есть в таблице отклонений и в cycles.csv.</p>`, autoSkipped)
		}
	}

	if len(b.Bad) > 0 {
		sb.WriteString(`<h2>Нечитаемые файлы</h2><div class="box">`)
		for _, s := range b.Bad {
			sb.WriteString(esc(s) + `<br>`)
		}
		sb.WriteString(`</div>`)
	}
	sb.WriteString(`</html>`)
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
