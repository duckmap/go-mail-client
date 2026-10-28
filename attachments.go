package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SaveAttachments сохраняет вложения в каталог dir, возвращает имена файлов.
func SaveAttachments(dir string, atts []Attachment) ([]string, error) {
	if dir == "" {
		dir = "."
	}
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("каталог %s: %w", dir, err)
	}
	var saved []string
	for i, a := range atts {
		name := sanitizeFilename(a.Filename)
		if name == "" {
			name = fmt.Sprintf("attachment-%d", i+1)
		}
		path := filepath.Join(dir, name)
		// уникальное имя, если файл уже существует
		for n := 1; fileExists(path); n++ {
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			path = filepath.Join(dir, fmt.Sprintf("%s(%d)%s", base, n, ext))
		}
		if err := os.WriteFile(path, a.Data, 0o644); err != nil {
			return saved, fmt.Errorf("запись %s: %w", path, err)
		}
		saved = append(saved, path)
	}
	return saved, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// sanitizeFilename убирает из имени опасные символы.
func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == 0 || r < 32:
			b.WriteRune('_')
		case r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "." || out == ".." {
		return ""
	}
	return out
}
