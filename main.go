package main

// shpscan — анализ файлов регистратора .shp.
//
//	shpscan                 просмотр в браузере: выбор файлов, графики, сравнение циклов
//	shpscan файл.shp        отчёт по одному циклу
//	shpscan папка           сводка по всем .shp в папке и подпапках + cycles.csv

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	out := flag.String("out", ".", "папка для отчётов")
	tzHours := flag.Float64("tz", 5, "смещение местного времени от UTC, часы")
	addr := flag.String("addr", "127.0.0.1:8765", "адрес страницы просмотра")
	flag.BoolVar(&AllSignals, "all", true, "считать показатели по всем сигналам файла (-all=false — только по спискам профиля)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Использование:")
		fmt.Fprintln(os.Stderr, "  shpscan                                  просмотр в браузере")
		fmt.Fprintln(os.Stderr, "  shpscan [-out папка] <файл.shp | папка>  отчёт в файл")
		flag.PrintDefaults()
	}
	flag.Parse()
	tz := time.Duration(*tzHours * float64(time.Hour))
	var err error
	switch flag.NArg() {
	case 0:
		err = Serve(*addr, tz)
	case 1:
		err = run(flag.Arg(0), *out, tz)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
}

func run(target, out string, tz time.Duration) error {
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if !info.IsDir() {
		f, err := ReadFile(target)
		if err != nil {
			return err
		}
		c := Analyze(f, filepath.Base(target), tz)
		name := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target)) + ".html"
		path := filepath.Join(out, name)
		if err := WriteCycleReport(path, f, c, tz); err != nil {
			return err
		}
		fmt.Println("Отчёт:", path)
		return nil
	}

	files, err := findFiles(target)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("в %s нет файлов .shp", target)
	}
	fmt.Printf("Найдено файлов: %d\n", len(files))
	started := time.Now()
	b := RunBatch(files, tz)
	if len(b.Cycles) == 0 {
		return fmt.Errorf("ни один файл не прочитан, первая ошибка: %s", b.Bad[0])
	}
	csvPath, htmlPath := filepath.Join(out, "cycles.csv"), filepath.Join(out, "report.html")
	if err := b.WriteCSV(csvPath); err != nil {
		return err
	}
	if err := WriteBatchReport(htmlPath, b, tz); err != nil {
		return err
	}
	anomalies := 0
	for _, g := range b.Groups {
		fmt.Printf("  %s: %d циклов, отклонений: %d\n", g.Name, len(g.Cycles), len(g.Anomalies))
		anomalies += len(g.Anomalies)
	}
	fmt.Printf("Всего циклов: %d, нечитаемых файлов: %d, отклонений: %d, время: %s\n",
		len(b.Cycles), len(b.Bad), anomalies, time.Since(started).Round(time.Millisecond))
	fmt.Println("Сводка:  ", htmlPath)
	fmt.Println("Таблица: ", csvPath)
	return nil
}
