package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSynthetic создаёт маленький .shp: 8 битов, 2 аналога, n отсчётов по 20 мс.
// Аналог 0 (ActualStep) = 1 в первой половине и 2 во второй, аналог 1 (MRF_Force_F) = номер отсчёта.
func writeSynthetic(t *testing.T, path string, n int) {
	t.Helper()
	le := binary.LittleEndian
	buf := make([]byte, headerSize+10*nameSize)
	le.PutUint64(buf[8:], 116444736000000000+uint64(1700000000)*10000000) // 14.11.2023 22:13:20 UTC
	le.PutUint32(buf[0x10:], 8)
	le.PutUint32(buf[0x2C:], 2)
	copy(buf[headerSize+0*nameSize:], "fi_TestFault")
	copy(buf[headerSize+8*nameSize:], "ActualStep")
	copy(buf[headerSize+9*nameSize:], "MRF_Force_F")
	for i := 0; i < n; i++ {
		rec := make([]byte, 4+1+8)
		le.PutUint32(rec, uint32(i*20))
		if i == 3 {
			rec[4] = 1 // бит 0 сработал на один отсчёт
		}
		step := float32(1)
		if i >= n/2 {
			step = 2
		}
		le.PutUint32(rec[5:], math.Float32bits(step))
		le.PutUint32(rec[9:], math.Float32bits(float32(i)))
		buf = append(buf, rec...)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadAndAnalyze(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.shp")
	writeSynthetic(t, path, 100)

	f, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.N != 100 || len(f.BitNames) != 8 || len(f.AnalogNames) != 2 {
		t.Fatalf("N=%d bits=%d analogs=%d", f.N, len(f.BitNames), len(f.AnalogNames))
	}
	if got := f.Start.Unix(); got != 1700000000 {
		t.Errorf("start = %d", got)
	}
	if v := f.Analog("MRF_Force_F"); v[0] != 0 || v[99] != 99 {
		t.Errorf("force = %v..%v", v[0], v[99])
	}
	if f.Analog("нет такого") != nil {
		t.Error("для отсутствующего сигнала ожидался nil")
	}

	c := Analyze(f, "26.000001.000002_OP3_x.shp", 5*time.Hour)
	if c.Profile != &KPS || c.Group != "КПС · OP3" {
		t.Errorf("profile = %s, group = %s", c.Profile.Name, c.Group)
	}
	if len(c.Steps) != 2 || c.Steps[0].Step != 1 || c.Steps[1].Step != 2 {
		t.Fatalf("steps = %+v", c.Steps)
	}
	if math.Abs(c.Steps[0].Dur-1.0) > 1e-9 {
		t.Errorf("длительность шага 1 = %v, ожидалась 1.0 с", c.Steps[0].Dur)
	}
	if v, _ := c.Get("MRF_Force_F.max"); v != 99 {
		t.Errorf("max = %v", v)
	}
	// Порог нагрузки 10 % от 99 = 9.9 → окно с отсчёта 10 по 99, то есть 89 × 20 мс.
	if v, _ := c.Get("t.load_s"); math.Abs(v-1.78) > 1e-9 {
		t.Errorf("t.load_s = %v", v)
	}
	if len(c.Faults) != 1 || c.Faults[0] != "fi_TestFault" {
		t.Errorf("faults = %v", c.Faults)
	}
	if c.Shift != "B" { // 22:13 UTC + 5 ч = 03:13 — ночная смена
		t.Errorf("shift = %s", c.Shift)
	}
}

func TestRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.shp")
	os.WriteFile(path, make([]byte, 5000), 0o644)
	if _, err := ReadFile(path); err == nil {
		t.Error("ожидалась ошибка на файле из нулей")
	}
}

func TestBatchFindsOutlier(t *testing.T) {
	b := &Group{}
	for i := 0; i < 40; i++ {
		v := 100 + float64(i%5) // обычный разброс 100..104
		if i == 17 {
			v = 180
		}
		c := &Cycle{ID: "c"}
		c.add("x", v)
		b.Cycles = append(b.Cycles, c)
	}
	b.computeStats()
	if len(b.Anomalies) != 1 || b.Anomalies[0].Cycle != b.Cycles[17] {
		t.Fatalf("аномалий: %d", len(b.Anomalies))
	}
}

// Сигнал, который всегда стоит на нуле и один раз «ожил», должен попасть в отклонения,
// хотя медианный разброс у него нулевой.
func TestBatchFindsWakeUpOfQuietSignal(t *testing.T) {
	g := &Group{}
	for i := 0; i < 40; i++ {
		c := &Cycle{ID: "c"}
		v := 0.0
		if i == 30 {
			v = 5
		}
		c.add("all.quiet.max", v)
		g.Cycles = append(g.Cycles, c)
	}
	g.computeStats()
	if len(g.Anomalies) != 1 || g.Anomalies[0].Cycle != g.Cycles[30] {
		t.Fatalf("аномалий: %d", len(g.Anomalies))
	}
}

func TestAllSignals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.shp")
	writeSynthetic(t, path, 100)
	f, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c := Analyze(f, "a.shp", 0)
	if v, ok := c.Get("all.ActualStep.mean"); !ok || v != 1.5 {
		t.Errorf("all.ActualStep.mean = %v, %v", v, ok)
	}
	if v, ok := c.Get("all.ActualStep.max"); !ok || v != 2 {
		t.Errorf("all.ActualStep.max = %v, %v", v, ok)
	}
	if _, dup := c.Get("all.MRF_Force_F.max"); dup {
		t.Error("максимум усилия уже есть в показателях профиля и не должен дублироваться")
	}
	if v, _ := c.Get("bit.fi_TestFault.on_s"); math.Abs(v-0.02) > 1e-9 { // один отсчёт по 20 мс
		t.Errorf("bit on_s = %v", v)
	}
	if v, _ := c.Get("bit.fi_TestFault.n"); v != 1 {
		t.Errorf("bit n = %v", v)
	}
}

func TestMedian(t *testing.T) {
	if m := median([]float64{5, 1, 3}); m != 3 {
		t.Errorf("нечётное: %v", m)
	}
	if m := median([]float64{4, 1, 3, 2}); m != 2.5 {
		t.Errorf("чётное: %v", m)
	}
}
