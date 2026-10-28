package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

const appVersion = "1.0.0"

func main() {
	cfgPath := flag.String("config", "", "путь к файлу конфигурации (JSON)")
	demo := flag.Bool("demo", false, "демонстрационный режим: встроенный почтовый сервер с примерами писем")
	ver := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *ver {
		fmt.Println("gomail", appVersion)
		return
	}

	var (
		cfg      *Config
		realPath string
		err      error
	)

	if *demo {
		acct, shutdown, derr := StartDemoServers()
		if derr != nil {
			fmt.Fprintln(os.Stderr, "Не удалось запустить демо-серверы:", derr)
			os.Exit(1)
		}
		defer shutdown()
		cfg = &Config{
			Version:       1,
			Accounts:      []AccountConfig{acct},
			AttachmentDir: DefaultAttachmentDir(),
			RefreshSec:    30,
			FetchLimit:    200,
		}
		realPath = filepath.Join(os.TempDir(), "gomail-demo-config.json")
		fmt.Fprintln(os.Stderr, "Демо-режим: запущены локальные IMAP/SMTP-серверы с примерами писем.")
	} else {
		if *cfgPath == "" {
			realPath = DefaultConfigPath()
		} else {
			realPath = *cfgPath
		}
		cfg, err = LoadConfig(realPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Ошибка конфигурации:", err)
			os.Exit(1)
		}
	}

	m := newModel(cfg, realPath)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
	if fm, ok := final.(model); ok {
		for _, a := range fm.accounts {
			a.Close()
		}
	}
}
