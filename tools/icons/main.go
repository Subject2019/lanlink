// 生成 LanLink 图标（纯标准库绘制，超采样抗锯齿）。
// 用法：cd tools/icons && go run .        （-root 指定仓库根目录，默认 ../..）
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const samples = 6 // 每个像素 6x6 超采样

type rgba struct{ r, g, b float64 }

var top = rgba{0.23, 0.51, 0.96}    // #3b82f6
var bottom = rgba{0.11, 0.31, 0.85} // #1d4ed8
var white = rgba{1, 1, 1}

func main() {
	root := flag.String("root", "../..", "仓库根目录")
	flag.Parse()

	targets := map[string]int{
		"deploy/fnos/lanlink/ICON.PNG":                                   64,
		"deploy/fnos/lanlink/ICON_256.PNG":                               256,
		"deploy/fnos/lanlink/app/ui/images/icon_64.png":                  64,
		"deploy/fnos/lanlink/app/ui/images/icon_256.png":                 256,
		"client/android/app/src/main/res/mipmap-mdpi/ic_launcher.png":    48,
		"client/android/app/src/main/res/mipmap-hdpi/ic_launcher.png":    72,
		"client/android/app/src/main/res/mipmap-xhdpi/ic_launcher.png":   96,
		"client/android/app/src/main/res/mipmap-xxhdpi/ic_launcher.png":  144,
		"client/android/app/src/main/res/mipmap-xxxhdpi/ic_launcher.png": 192,
	}
	for path, size := range targets {
		if err := save(filepath.Join(*root, path), size); err != nil {
			panic(err)
		}
		println("wrote", path, size)
	}
	if err := saveICO(filepath.Join(*root, "client/windows/assets/icon.ico"), 64); err != nil {
		panic(err)
	}
	println("wrote client/windows/assets/icon.ico")
}

// saveICO 生成标准 32bpp BMP 格式 .ico（Windows 托盘 LoadImage 需要）。
func saveICO(path string, size int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	img := render(size)
	rowBytes := size * 4
	maskRow := ((size + 31) / 32) * 4
	body := new(bytes.Buffer)

	hdr := make([]byte, 40)
	binary.LittleEndian.PutUint32(hdr[0:], 40)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(size))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(size*2)) // XOR + AND
	binary.LittleEndian.PutUint16(hdr[12:], 1)
	binary.LittleEndian.PutUint16(hdr[14:], 32)
	binary.LittleEndian.PutUint32(hdr[20:], uint32(rowBytes*size+maskRow*size))
	body.Write(hdr)

	// BMP 是自下而上的 BGRA
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			c := img.NRGBAAt(x, y)
			body.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	body.Write(make([]byte, maskRow*size)) // AND mask 全 0 = 完全不透明

	ico := new(bytes.Buffer)
	binary.Write(ico, binary.LittleEndian, uint16(0))
	binary.Write(ico, binary.LittleEndian, uint16(1))
	binary.Write(ico, binary.LittleEndian, uint16(1))
	ico.Write([]byte{byte(size), byte(size), 0, 0})
	binary.Write(ico, binary.LittleEndian, uint16(1))
	binary.Write(ico, binary.LittleEndian, uint16(32))
	binary.Write(ico, binary.LittleEndian, uint32(body.Len()))
	binary.Write(ico, binary.LittleEndian, uint32(22))
	ico.Write(body.Bytes())
	return os.WriteFile(path, ico.Bytes(), 0o644)
}

func save(path string, size int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, render(size))
}

func render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	step := 1.0 / (float64(size) * samples)
	var bg rgba
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var sr, sg, sb, sa float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := (float64(x)*samples + float64(sx) + 0.5) * step
					py := (float64(y)*samples + float64(sy) + 0.5) * step
					if !inRoundRect(px, py) {
						continue
					}
					bg = mix(top, bottom, py)
					if inArrowRight(px, py) || inArrowLeft(px, py) {
						bg = white
					}
					sr += bg.r
					sg += bg.g
					sb += bg.b
					sa += 1
				}
			}
			n := float64(samples * samples)
			if sa == 0 {
				img.SetNRGBA(x, y, color.NRGBA{})
				continue
			}
			// 预乘后再还原，避免边缘发黑
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(clamp(sr/sa) * 255),
				G: uint8(clamp(sg/sa) * 255),
				B: uint8(clamp(sb/sa) * 255),
				A: uint8(sa / n * 255),
			})
		}
	}
	return img
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func mix(a, b rgba, t float64) rgba {
	return rgba{
		r: a.r + (b.r-a.r)*t,
		g: a.g + (b.g-a.g)*t,
		b: a.b + (b.b-a.b)*t,
	}
}

// 圆角方块：中心 0.5，半径 0.5，圆角 0.22
func inRoundRect(px, py float64) bool {
	const half, r = 0.5, 0.22
	qx := math.Abs(px-0.5) - half + r
	qy := math.Abs(py-0.5) - half + r
	d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
	return d <= 0
}

// 上面的箭头指向右
func inArrowRight(x, y float64) bool {
	if capsule(x, y, 0.26, 0.375, 0.68, 0.375, 0.045) {
		return true
	}
	return triangle(x, y, 0.62, 0.265, 0.62, 0.485, 0.80, 0.375)
}

// 下面的箭头指向左
func inArrowLeft(x, y float64) bool {
	if capsule(x, y, 0.32, 0.625, 0.74, 0.625, 0.045) {
		return true
	}
	return triangle(x, y, 0.38, 0.515, 0.38, 0.735, 0.20, 0.625)
}

// capsule 判断点是否落在线段（带圆头）内
func capsule(x, y, x1, y1, x2, y2, r float64) bool {
	dx, dy := x2-x1, y2-y1
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = ((x-x1)*dx + (y-y1)*dy) / l2
		t = math.Max(0, math.Min(1, t))
	}
	cx, cy := x1+t*dx, y1+t*dy
	return math.Hypot(x-cx, y-cy) <= r
}

func triangle(x, y, ax, ay, bx, by, cx, cy float64) bool {
	d1 := (x-bx)*(ay-by) - (ax-bx)*(y-by)
	d2 := (x-cx)*(by-cy) - (bx-cx)*(y-cy)
	d3 := (x-ax)*(cy-ay) - (cx-ax)*(y-ay)
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}
