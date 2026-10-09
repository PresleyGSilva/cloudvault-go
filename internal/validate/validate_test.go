package validate

import "testing"

func TestFileNameTraversalRejected(t *testing.T) {
	bs := string([]byte{0x5c}) // backslash
	nul := string([]byte{0x00})
	soh := string([]byte{0x01})
	cases := []string{"", ".", "..", "../secret", "a/b", "a" + bs + "b", ".." + bs + "x", "x/../y", "a" + nul + ".png", "a" + soh + "b"}
	for _, c := range cases {
		if err := ValidateFileName(c); err == nil {
			t.Errorf("expected rejection for %q", c)
		}
	}
	good := []string{"report.pdf", "photo 2024-01.png", "nota_fiscal (1).pdf", ".hidden", "a..b.txt"}
	for _, c := range good {
		if err := ValidateFileName(c); err != nil {
			t.Errorf("expected acceptance for %q: %v", c, err)
		}
	}
}

func TestBackslashRejected(t *testing.T) {
	if err := ValidateFileName("a" + string([]byte{0x5c}) + "b"); err == nil {
		t.Error("expected backslash rejection")
	}
}

func TestBlockedTypes(t *testing.T) {
	for _, m := range []string{"application/x-msdownload", "application/x-sh", "application/x-elf"} {
		if err := AllowedType(m); err == nil {
			t.Errorf("expected block for %s", m)
		}
	}
	if err := AllowedType("image/png"); err != nil {
		t.Errorf("png should be allowed: %v", err)
	}
}

func TestKind(t *testing.T) {
	if Kind("image/png") != "image" || Kind("application/pdf") != "pdf" || Kind("video/mp4") != "video" {
		t.Error("kind mapping broken")
	}
}

func TestSizeBounds(t *testing.T) {
	if MaxFileBytes != 200<<20 || MaxProxyBytes != 15<<20 {
		t.Error("size limits changed unexpectedly")
	}
}
