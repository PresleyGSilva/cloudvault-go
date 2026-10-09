package validate

import (
	"fmt"
	"strings"
	"unicode"
)

const MaxFileBytes = 200 << 20 // 200 MiB (multipart/presigned for large files)
const MaxProxyBytes = 15 << 20 // 15 MiB through the API proxy

var blockedMimes = map[string]bool{
	"application/x-msdownload":                      true,
	"application/x-msdos-program":                   true,
	"application/x-sh":                              true,
	"application/x-executable":                      true,
	"application/x-elf":                             true,
	"application/vnd.microsoft.portable-executable": true,
}

// ValidateFileName enforces anti path-traversal + sane names.
func ValidateFileName(name string) error {
	if name == "" || len(name) > 255 {
		return fmt.Errorf("name must be 1..255 chars")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid name")
	}
	if strings.HasPrefix(name, "..") || strings.HasSuffix(name, "..") || strings.Contains(name, "../") || strings.Contains(name, "..\\") {
		return fmt.Errorf("name must not contain traversal sequences")
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "\x00") {
		return fmt.Errorf("name must not contain paths or traversal sequences")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("name must not contain control characters")
		}
	}
	return nil
}

func ValidateFolderName(name string) error { return ValidateFileName(name) }

// SanitizeForKey strips anything unsafe for the object key suffix.
func SanitizeForKey(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	s := strings.Trim(b.String(), " .")
	if s == "" || s == "." || s == ".." {
		return "file"
	}
	return s
}

func AllowedType(mime string) error {
	m := strings.ToLower(strings.TrimSpace(mime))
	if m == "" {
		return fmt.Errorf("content type is required")
	}
	if blockedMimes[m] {
		return fmt.Errorf("file type not allowed")
	}
	return nil
}

// Kind maps a mime to a filter bucket.
func Kind(mime string) string {
	m := strings.ToLower(mime)
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "video/"):
		return "video"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	case m == "application/pdf":
		return "pdf"
	case strings.HasPrefix(m, "text/") || strings.Contains(m, "document") || strings.Contains(m, "sheet") || strings.Contains(m, "presentation") || strings.Contains(m, "msword") || strings.Contains(m, "officedocument"):
		return "doc"
	default:
		return "other"
	}
}
