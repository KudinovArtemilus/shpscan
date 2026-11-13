package main

// shp.go — чтение файла регистратора .shp.
//
// Структура файла (все числа little-endian):
//
//	0x000  заголовок, 892 байта:
//	         0x08  время старта записи, Windows FILETIME (UTC)
//	         0x10  количество дискретных сигналов (битов)
//	         0x2C  количество аналоговых сигналов (float32)
//	0x37C  таблица имён: (биты + аналоги) записей по 256 байт, строка до нулевого байта
//	далее  отсчёты подряд, каждый отсчёт:
//	         uint32   время от старта, мс
//	         биты     по 1 биту на сигнал (младший бит первый)
//	         float32  по 4 байта на аналоговый сигнал

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

const (
	headerSize = 0x37C
	nameSize   = 256
)

// File — разобранный .shp. Отсчёты остаются в сыром виде (raw),
// нужный сигнал декодируется только по запросу — так быстрее на больших архивах.
type File struct {
	Path        string
	Start       time.Time // время старта записи, UTC
	N           int       // количество отсчётов
	BitNames    []string
	AnalogNames []string

	raw     []byte
	recSize int
	bitIdx  map[string]int
	anIdx   map[string]int
}

func ReadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < headerSize {
		return nil, fmt.Errorf("файл короче заголовка (%d байт)", len(data))
	}
	le := binary.LittleEndian
	nBits := int(le.Uint32(data[0x10:]))
	nAn := int(le.Uint32(data[0x2C:]))
	if nBits%8 != 0 || nBits > 1<<16 || nAn <= 0 || nAn > 1<<16 {
		return nil, fmt.Errorf("незнакомый заголовок: битов=%d, аналогов=%d", nBits, nAn)
	}
	namesEnd := headerSize + (nBits+nAn)*nameSize
	rec := 4 + nBits/8 + nAn*4
	if len(data) < namesEnd+2*rec {
		return nil, fmt.Errorf("в файле нет данных (размер %d байт)", len(data))
	}
	f := &File{
		Path:    path,
		raw:     data[namesEnd:],
		recSize: rec,
		bitIdx:  map[string]int{},
		anIdx:   map[string]int{},
	}
	f.N = len(f.raw) / rec // неполный последний отсчёт (оборванная запись) отбрасываем

	// FILETIME — сотни наносекунд от 01.01.1601
	ft := int64(le.Uint64(data[8:]))
	f.Start = time.Unix(0, (ft-116444736000000000)*100).UTC()

	for i := 0; i < nBits+nAn; i++ {
		name := cString(data[headerSize+i*nameSize : headerSize+(i+1)*nameSize])
		if i < nBits {
			f.BitNames = append(f.BitNames, name)
			if _, dup := f.bitIdx[name]; !dup {
				f.bitIdx[name] = i
			}
		} else {
			f.AnalogNames = append(f.AnalogNames, name)
			if _, dup := f.anIdx[name]; !dup {
				f.anIdx[name] = i - nBits
			}
		}
	}

	// Проверка, что раскладка угадана верно: время должно расти.
	if t0, t1 := le.Uint32(f.raw), le.Uint32(f.raw[rec:]); t1 <= t0 {
		return nil, fmt.Errorf("время в отсчётах не растёт (%d, %d) — другой формат файла?", t0, t1)
	}
	return f, nil
}

// cString берёт строку до нулевого байта и переводит Latin-1 в UTF-8.
func cString(b []byte) string {
	r := make([]rune, 0, 32)
	for _, c := range b {
		if c == 0 {
			break
		}
		r = append(r, rune(c))
	}
	return strings.TrimSpace(string(r))
}

// Time возвращает время каждого отсчёта в секундах от старта записи.
func (f *File) Time() []float64 {
	t := make([]float64, f.N)
	for i := range t {
		t[i] = float64(binary.LittleEndian.Uint32(f.raw[i*f.recSize:])) / 1000
	}
	return t
}

// Analog возвращает значения аналогового сигнала по имени или nil, если такого нет.
func (f *File) Analog(name string) []float64 {
	i, ok := f.anIdx[name]
	if !ok {
		return nil
	}
	return f.AnalogAt(i)
}

func (f *File) AnalogAt(i int) []float64 {
	off := 4 + len(f.BitNames)/8 + i*4
	v := make([]float64, f.N)
	for k := range v {
		u := binary.LittleEndian.Uint32(f.raw[k*f.recSize+off:])
		x := float64(math.Float32frombits(u))
		if math.IsNaN(x) || math.IsInf(x, 0) {
			x = 0
		}
		v[k] = x
	}
	return v
}

// BitAt возвращает значения дискретного сигнала с номером i.
func (f *File) BitAt(i int) []bool {
	off, mask := 4+i/8, byte(1)<<(i%8)
	v := make([]bool, f.N)
	for k := range v {
		v[k] = f.raw[k*f.recSize+off]&mask != 0
	}
	return v
}
