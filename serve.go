package main

// serve.go — режим просмотра в браузере: маленький веб-сервер на этом же компьютере.
// Выбранные на странице файлы копируются во временную папку, по каждому сразу
// считаются показатели, а значения сигналов отдаются странице по запросу.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui.html
var uiHTML []byte

type loaded struct {
	id      int
	path    string // временная копия файла
	cycle   *Cycle
	analogs []string
	bits    []string
}

type server struct {
	mu     sync.Mutex
	dir    string
	nextID int
	files  []*loaded
	tz     time.Duration
}

// То, что уходит странице в виде JSON.
type fileDTO struct {
	ID       int                `json:"id"`
	File     string             `json:"file"`
	Group    string             `json:"group"`
	CycleID  string             `json:"cycleId"`
	Start    string             `json:"start"`
	Shift    string             `json:"shift"`
	Duration float64            `json:"duration"`
	LoadT0   float64            `json:"loadT0"`
	Steps    []StepSpan         `json:"steps"`
	KPIs     map[string]float64 `json:"kpis"`
	Faults   []string           `json:"faults"`
}

type statDTO struct {
	N      int     `json:"n"`
	Median float64 `json:"median"`
	Sigma  float64 `json:"sigma"`
}

type stateDTO struct {
	Files      []fileDTO                     `json:"files"`
	Analogs    []string                      `json:"analogs"`
	Bits       []string                      `json:"bits"`
	Groups     []ChartGroup                  `json:"groups"`
	KPIOrder   []string                      `json:"kpiOrder"`
	Stats      map[string]map[string]statDTO `json:"stats"` // группа -> показатель -> норма
	Errors     []string                      `json:"errors"`
	MinForNorm int                           `json:"minForNorm"`
	ZLimit     float64                       `json:"zLimit"`
	ZLimitAuto float64                       `json:"zLimitAuto"`
}

// state собирает текущее состояние. Вызывать под s.mu.
func (s *server) state(errs []string) stateDTO {
	sort.SliceStable(s.files, func(i, j int) bool { return s.files[i].cycle.Start.Before(s.files[j].cycle.Start) })
	st := stateDTO{
		Files: []fileDTO{}, Analogs: []string{}, Bits: []string{}, KPIOrder: []string{},
		Groups: []ChartGroup{}, Stats: map[string]map[string]statDTO{}, Errors: errs,
		MinForNorm: minForNorm, ZLimit: zLimit, ZLimitAuto: zLimitAuto,
	}
	var cycles []*Cycle
	seenA, seenB, seenP := map[string]bool{}, map[string]bool{}, map[*Profile]bool{}
	for _, l := range s.files {
		c := l.cycle
		cycles = append(cycles, c)
		if !seenP[c.Profile] { // готовые наборы сигналов — от всех машин, чьи файлы загружены
			seenP[c.Profile] = true
			st.Groups = append(st.Groups, c.Profile.Charts...)
		}
		d := fileDTO{
			ID: l.id, File: c.File, Group: c.Group, CycleID: c.ID, Shift: c.Shift, Duration: c.Duration, LoadT0: c.LoadT0,
			Start: c.Start.Add(s.tz).Format("02.01.2006 15:04:05"),
			Steps: c.Steps, KPIs: map[string]float64{}, Faults: c.Faults,
		}
		for _, k := range c.KPIs {
			d.KPIs[k.Key] = k.Value
		}
		st.Files = append(st.Files, d)
		for _, n := range l.analogs {
			if !seenA[n] {
				seenA[n] = true
				st.Analogs = append(st.Analogs, n)
			}
		}
		for _, n := range l.bits {
			if !seenB[n] {
				seenB[n] = true
				st.Bits = append(st.Bits, n)
			}
		}
	}
	st.KPIOrder = append(st.KPIOrder, kpiKeys(cycles)...)
	for _, g := range groupCycles(cycles) { // та же норма, что и в сводке по папке
		m := map[string]statDTO{}
		for _, x := range g.Stats {
			if !strings.HasPrefix(x.Key, "cnt.") {
				m[x.Key] = statDTO{x.N, x.Median, x.Sigma}
			}
		}
		st.Stats[g.Name] = m
	}
	return st
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.state(nil))
}

// handleUpload принимает файлы со страницы. Каждый пишется сразу на диск,
// поэтому память не растёт, сколько бы файлов ни выбрали.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			errs = append(errs, err.Error())
			break
		}
		name := filepath.Base(part.FileName())
		if !strings.EqualFold(filepath.Ext(name), ".shp") {
			continue
		}
		s.nextID++
		tmp := filepath.Join(s.dir, fmt.Sprintf("%06d.shp", s.nextID))
		if err := saveTo(tmp, part); err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
		f, err := ReadFile(tmp)
		if err != nil {
			os.Remove(tmp)
			errs = append(errs, name+": "+err.Error())
			continue
		}
		c := Analyze(f, name, s.tz)
		s.removeWhere(func(l *loaded) bool { return l.cycle.File == name }) // повторно выбранный файл заменяет старый
		s.files = append(s.files, &loaded{s.nextID, tmp, c, f.AnalogNames, f.BitNames})
	}
	writeJSON(w, s.state(errs))
}

func saveTo(path string, src io.Reader) error {
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

func (s *server) removeWhere(match func(*loaded) bool) {
	kept := s.files[:0]
	for _, l := range s.files {
		if match(l) {
			os.Remove(l.path)
		} else {
			kept = append(kept, l)
		}
	}
	s.files = kept
}

// handleRemove убирает файлы из списка: {"ids":[1,2]}, пустой список — убрать все.
func (s *server) handleRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int `json:"ids"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	drop := map[int]bool{}
	for _, id := range req.IDs {
		drop[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeWhere(func(l *loaded) bool { return len(drop) == 0 || drop[l.id] })
	writeJSON(w, s.state(nil))
}

func (s *server) find(id int) *loaded {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.files {
		if l.id == id {
			return l
		}
	}
	return nil
}

// handleSignals отдаёт значения сигналов одного файла.
// Имена приходят с префиксом: "a:" — аналоговый, "b:" — дискретный.
func (s *server) handleSignals(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    int      `json:"id"`
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	l := s.find(req.ID)
	if l == nil {
		http.Error(w, "файл не найден", http.StatusNotFound)
		return
	}
	f, err := ReadFile(l.path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := struct {
		T []float32            `json:"t"`
		Y map[string][]float32 `json:"y"`
	}{toF32(f.Time()), map[string][]float32{}}
	for _, key := range req.Names {
		name := key[strings.Index(key, ":")+1:]
		if strings.HasPrefix(key, "b:") {
			if i, ok := f.bitIdx[name]; ok {
				v := make([]float32, f.N)
				for k, on := range f.BitAt(i) {
					if on {
						v[k] = 1
					}
				}
				resp.Y[key] = v
			}
		} else if v := f.Analog(name); v != nil {
			resp.Y[key] = toF32(v)
		}
	}
	writeJSON(w, resp)
}

func toF32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

// handleReport — полный отчёт по одному файлу, тот же, что пишет «shpscan файл.shp».
func (s *server) handleReport(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	l := s.find(id)
	if l == nil {
		http.NotFound(w, r)
		return
	}
	f, err := ReadFile(l.path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, CycleReportHTML(f, l.cycle, s.tz))
}

// Serve запускает сервер и открывает страницу в браузере.
func Serve(addr string, tz time.Duration) error {
	dir, err := os.MkdirTemp("", "shpscan-")
	if err != nil {
		return err
	}
	s := &server{dir: dir, tz: tz}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	})
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/remove", s.handleRemove)
	mux.HandleFunc("/api/signals", s.handleSignals)
	mux.HandleFunc("/report", s.handleReport)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("не удалось занять %s (программа уже запущена?): %v", addr, err)
	}
	url := "http://" + ln.Addr().String()
	fmt.Println("Просмотр открыт в браузере:", url)
	fmt.Println("Чтобы закончить, закройте это окно или нажмите Ctrl+C.")

	// По Ctrl+C убираем временные копии файлов.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	go func() {
		<-stop
		os.RemoveAll(dir)
		os.Exit(0)
	}()

	openBrowser(url)
	return http.Serve(ln, mux)
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}
