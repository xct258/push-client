package gui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"golang.org/x/image/font/sfnt"
)

var (
	cjkOnce sync.Once
	cjkRes  fyne.Resource
)

// cjkTheme 在默认主题基础上叠加中文字体。
//
// 注意：Fyne 底层字体引擎不支持 .ttc 字体集合（加载直接报错甚至崩溃），
// 因此这里只接受单文件 .ttf/.otf，并用 sfnt 校验确含 CJK 字形才采用。
type cjkTheme struct {
	fyne.Theme
}

func CJKTheme() fyne.Theme {
	return &cjkTheme{Theme: theme.DefaultTheme()}
}

func (t *cjkTheme) Font(s fyne.TextStyle) fyne.Resource {
	if r := cjkFont(); r != nil {
		return r
	}
	return t.Theme.Font(s)
}

func cjkFont() fyne.Resource {
	cjkOnce.Do(func() {
		for _, p := range fontCandidates() {
			data, err := os.ReadFile(p)
			if err != nil || !isSingleFont(data) {
				continue
			}
			if !hasCJK(data) {
				continue
			}
			cjkRes = fyne.NewStaticResource(filepath.Base(p), data)
			return
		}
	})
	return cjkRes
}

func fontCandidates() []string {
	var out []string
	if p := strings.TrimSpace(os.Getenv("PUSH_CLIENT_FONT")); p != "" {
		out = append(out, p)
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, "push-client-font.ttf"))
	}
	// fontconfig 发现的中文字体（Linux 通用，macOS 若有也顺带）
	out = append(out, fontconfigCJK()...)
	// Windows（均为单文件 ttf：等线/黑体，系统自带）
	winDir := os.Getenv("WINDIR")
	if winDir == "" {
		winDir = `C:\Windows`
	}
	fonts := filepath.Join(winDir, "Fonts")
	out = append(out,
		filepath.Join(fonts, "deng.ttf"),
		filepath.Join(fonts, "dengb.ttf"),
		filepath.Join(fonts, "simhei.ttf"),
	)
	// Linux 常见单文件 CJK
	out = append(out,
		"/usr/share/fonts/opentype/ipaexfont-gothic/ipaexg.ttf",
		"/usr/share/fonts/opentype/ipaexfont-mincho/ipaexm.ttf",
	)
	// macOS 单文件备选
	out = append(out, "/Library/Fonts/Arial Unicode.ttf")
	return out
}

// fontconfigCJK 用 fc-list 找中文单文件字体（过滤 .ttc 集合与非 ttf/otf）。
func fontconfigCJK() []string {
	fc, err := exec.LookPath("fc-list")
	if err != nil {
		return nil
	}
	out, err := exec.Command(fc, ":lang=zh", "file").Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		p := strings.TrimSpace(strings.TrimSuffix(line, ":"))
		if p == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".ttf" && ext != ".otf" {
			continue
		}
		paths = append(paths, p)
	}
	return paths
}

// isSingleFont 是否单文件 ttf/otf（拒绝 ttcf 集合：Fyne 读集合必崩）。
func isSingleFont(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	return string(b[:4]) == "\x00\x01\x00\x00" || string(b[:4]) == "OTTO"
}

// hasCJK 用 sfnt 查码表，确认含常用汉字（避免载入纯西文字体导致满屏方框）。
func hasCJK(data []byte) bool {
	f, err := sfnt.Parse(data)
	if err != nil {
		return false
	}
	var buf sfnt.Buffer
	for _, r := range []rune{'中', '文', '提', '醒'} {
		idx, err := f.GlyphIndex(&buf, r)
		if err != nil || idx == 0 {
			return false
		}
	}
	return true
}

// FontStatus 供设置页显示当前字体来源，方便排查方框字。
func FontStatus() string {
	if r := cjkFont(); r != nil {
		return fmt.Sprintf("中文字体：%s", r.Name())
	}
	return "中文字体：未找到（中文可能显示为方框，可用 PUSH_CLIENT_FONT 指定 ttf/otf 字体文件）"
}
