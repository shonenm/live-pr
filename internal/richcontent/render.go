// Package richcontent prepares optional terminal-friendly GitHub content.
package richcontent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	mermaidFence = regexp.MustCompile("(?s)```mermaid[ \\t]*\\n(.*?)\\n```")
	avatarColors sync.Map
)

// MermaidSources returns the fenced Mermaid bodies in Markdown.
func MermaidSources(markdown string) []string {
	matches := mermaidFence.FindAllStringSubmatch(markdown, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, strings.TrimSpace(match[1]))
	}
	return out
}

// ReplaceMermaid replaces successfully rendered fences and leaves failures untouched.
func ReplaceMermaid(markdown string, rendered map[string]string) string {
	return mermaidFence.ReplaceAllStringFunc(markdown, func(fence string) string {
		match := mermaidFence.FindStringSubmatch(fence)
		source := strings.TrimSpace(match[1])
		if diagram := strings.TrimSpace(rendered[source]); diagram != "" {
			return "```text\n" + diagram + "\n```"
		}
		return fence
	})
}

// RenderMermaidContext renders within the caller's deadline.
func RenderMermaidContext(ctx context.Context, source string, width int) (string, error) {
	path, err := exec.LookPath("termaid")
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, "--width", fmt.Sprint(max(20, width)), "--padding-x", "1", "--padding-y", "0", "--gap", "1")
	cmd.Stdin = strings.NewReader(source)
	var output limitedBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 256 << 10
	if b.Len()+len(p) > limit {
		return 0, errors.New("rich content output exceeds 256 KiB")
	}
	return b.Buffer.Write(p)
}

// AvatarColorContext downloads and reduces an avatar within the caller's deadline.
func AvatarColorContext(ctx context.Context, rawURL string) (string, error) {
	if color, ok := avatarColors.Load(rawURL); ok {
		return color.(string), nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() != "avatars.githubusercontent.com" {
		return "", errors.New("unsupported avatar URL")
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.Hostname() != "avatars.githubusercontent.com" {
				return errors.New("avatar redirect rejected")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("avatar HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20+1))
	if err != nil || len(data) > 5<<20 {
		return "", errors.New("avatar exceeds 5 MiB")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return "", errors.New("invalid avatar dimensions")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	color := imageColor(img)
	avatarColors.Store(rawURL, color)
	return color, nil
}

func imageColor(img image.Image) string {
	const bgR, bgG, bgB = uint64(0x0d0d), uint64(0x1111), uint64(0x1717)
	const maxSamples = 4096
	var red, green, blue, count uint64
	bounds := img.Bounds()
	// Sample on a stride so a large avatar is averaged from at most a few
	// thousand pixels; the representative color is visually identical.
	area := int64(bounds.Dx()) * int64(bounds.Dy())
	step := 1
	if area > maxSamples {
		step = int(math.Sqrt(float64(area) / maxSamples))
		if step < 1 {
			step = 1
		}
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			r, g, b, a := img.At(x, y).RGBA()
			red += uint64(r) + bgR*(0xffff-uint64(a))/0xffff
			green += uint64(g) + bgG*(0xffff-uint64(a))/0xffff
			blue += uint64(b) + bgB*(0xffff-uint64(a))/0xffff
			count++
		}
	}
	if count == 0 {
		return "#8b949e"
	}
	return fmt.Sprintf("#%02x%02x%02x", red/count>>8, green/count>>8, blue/count>>8)
}
