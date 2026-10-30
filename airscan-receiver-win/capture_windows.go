//go:build windows

package main

import (
	"fmt"
	"image"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetDC                    = user32.NewProc("GetDC")
	procReleaseDC                = user32.NewProc("ReleaseDC")
	procPrintWindow              = user32.NewProc("PrintWindow")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procSetProcessDpiAwareness   = shcore.NewProc("SetProcessDpiAwareness")

	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")

	procCreateCompatibleDC         = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap     = gdi32.NewProc("CreateCompatibleBitmap")
	procCreateDIBSection           = gdi32.NewProc("CreateDIBSection")
	procSelectObject               = gdi32.NewProc("SelectObject")
	procBitBlt                     = gdi32.NewProc("BitBlt")
	procStretchBlt                 = gdi32.NewProc("StretchBlt")
	procDeleteObject               = gdi32.NewProc("DeleteObject")
	procDeleteDC                   = gdi32.NewProc("DeleteDC")
	procGetWindowDisplayAffinity   = user32.NewProc("GetWindowDisplayAffinity")
	procEnumChildWindows           = user32.NewProc("EnumChildWindows")
	procMonitorFromWindow          = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW            = user32.NewProc("GetMonitorInfoW")
)

const (
	PW_RENDERFULLCONTENT = 0x00000002
	SRCCOPY              = 0x00CC0020
	DIB_RGB_COLORS       = 0
	BI_RGB               = 0
	SM_CXSCREEN          = 0
	SM_CYSCREEN          = 1
	SM_XVIRTUALSCREEN    = 76
	SM_YVIRTUALSCREEN    = 77
	SM_CXVIRTUALSCREEN   = 78
	SM_CYVIRTUALSCREEN   = 79
	MONITOR_DEFAULTTONEAREST = 2

	PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

	// GDI rejects very large compatible bitmaps via GetDIBits. Cap the
	// captured bitmap and scale the window into it.
	maxCaptureSide = 2560
)

type RECT struct {
	Left, Top, Right, Bottom int32
}

type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type BITMAPINFO struct {
	BmiHeader BITMAPINFOHEADER
	BmiColors [1]uint32
}

type WindowInfo struct {
	Handle      uint64 `json:"handle"`
	Title       string `json:"title"`
	ProcessName string `json:"processName"`
	ClassName   string `json:"className"`
	Width       int32  `json:"width"`
	Height      int32  `json:"height"`
	// Affinity 为窗口的防截屏标志：0=无保护，0x1=WDA_MONITOR，0x11=WDA_EXCLUDEFROMCAPTURE。
	// 非零时所有用户态捕获均返回黑帧，前端应提示改用摄像头/手机扫码。
	Affinity uint32 `json:"affinity,omitempty"`
}

func initDPI() {
	if procSetProcessDpiAwareness.Find() == nil {
		procSetProcessDpiAwareness.Call(2)
	}
}

func getProcessName(hwnd syscall.Handle) string {
	var pid uint32
	procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}

	hProc, _, _ := procOpenProcess.Call(PROCESS_QUERY_LIMITED_INFORMATION, 0, uintptr(pid))
	if hProc == 0 {
		return ""
	}
	defer procCloseHandle.Call(hProc)

	var buf [512]uint16
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageNameW.Call(hProc, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret != 0 {
		full := syscall.UTF16ToString(buf[:size])
		return filepath.Base(full)
	}
	return ""
}

func getClassName(hwnd syscall.Handle) string {
	var buf [256]uint16
	procGetClassNameW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:])
}

// ListAllWindows 枚举窗口：穿透中兴云桌面、企业远程桌面、无标题或子类名窗口
func ListAllWindows() ([]WindowInfo, error) {
	var list []WindowInfo

	cb := syscall.NewCallback(func(hwnd syscall.Handle, lparam uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
		if vis == 0 {
			return 1
		}

		var rect RECT
		procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
		w := rect.Right - rect.Left
		h := rect.Bottom - rect.Top

		if w < 100 || h < 100 {
			return 1
		}

		length, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
		title := ""
		if length > 0 {
			buf := make([]uint16, length+1)
			procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
			title = syscall.UTF16ToString(buf)
		}

		cls := getClassName(hwnd)
		procName := getProcessName(hwnd)

		// 读取窗口防截屏标志：非零表示 DLP 保护，捕获必黑
		var aff uint32
		procGetWindowDisplayAffinity.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&aff)))

		// 过滤明显无意义系统桌面层
		if title == "Program Manager" || title == "Default IME" || title == "MSCTFIME UI" || cls == "Shell_TrayWnd" {
			return 1
		}

		// 如果没有标题，但是属于第三方进程（如中兴云桌面 ZTE / uSmartView / Cloud 等）或者大窗口，自动赋予易识别的名字
		displayTitle := title
		if displayTitle == "" {
			if procName != "" {
				displayTitle = fmt.Sprintf("[%s] %s", procName, cls)
			} else {
				displayTitle = fmt.Sprintf("窗口 (类名: %s)", cls)
			}
		} else if procName != "" {
			displayTitle = fmt.Sprintf("[%s] %s", procName, displayTitle)
		}

		list = append(list, WindowInfo{
			Handle:      uint64(hwnd),
			Title:       displayTitle,
			ProcessName: procName,
			ClassName:   cls,
			Width:       w,
			Height:      h,
			Affinity:    aff,
		})

		return 1
	})

	procEnumWindows.Call(cb, 0)
	return list, nil
}

// CaptureWindow captures one top-level window.
// uSmartView is usually fullscreen, GPU-rendered, and marked
// WDA_EXCLUDEFROMCAPTURE. GetDIBits on a selected compatible bitmap fails
// for that size, and GDI itself returns black. Try Graphics Capture first,
// then a DIB-section screen blit, then the largest child window.
func CaptureWindow(hwnd uint64) (image.Image, error) {
	handle := syscall.Handle(hwnd)
	img, err := captureWindowOnce(handle)
	if img != nil && !isImageBlank(img) {
		return img, nil
	}

	child := largestChild(handle)
	if child != 0 && child != handle {
		img2, err2 := captureWindowOnce(child)
		if img2 != nil && !isImageBlank(img2) {
			return img2, nil
		}
		if img == nil {
			img, err = img2, err2
		}
	}

	if img != nil {
		if err == nil {
			if note := affinityNote(handle); note != "" {
				err = fmt.Errorf("%s，抓到的画面是黑色", note)
			}
		}
		return img, err
	}
	if err == nil {
		err = fmt.Errorf("捕获失败")
	}
	if note := affinityNote(handle); note != "" {
		err = fmt.Errorf("%s: %w", note, err)
	}
	return nil, err
}

func captureWindowOnce(handle syscall.Handle) (image.Image, error) {
	var firstErr error
	remember := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if img, err := wgcCaptureWindow(handle); err == nil && img != nil && !isImageBlank(img) {
		return img, nil
	} else {
		remember(err)
	}

	img, err := captureRectFromScreen(handle)
	if err == nil && img != nil && !isImageBlank(img) {
		return img, nil
	}
	remember(err)
	var kept image.Image
	if err == nil {
		kept = img
	}

	if printed, err := capturePrintWindow(handle); err == nil && printed != nil && !isImageBlank(printed) {
		return printed, nil
	} else {
		remember(err)
		if kept == nil && err == nil {
			kept = printed
		}
	}

	if mon, err := wgcCaptureMonitorCrop(handle); err == nil && mon != nil && !isImageBlank(mon) {
		return mon, nil
	} else {
		remember(err)
		if kept == nil && err == nil {
			kept = mon
		}
	}

	if kept != nil {
		return kept, firstErr
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("没有可用的捕获结果")
	}
	return nil, firstErr
}

// CaptureScreen captures the virtual screen without GetDIBits.
func CaptureScreen() (image.Image, error) {
	hdcScreen, _, errn := procGetDC.Call(0)
	if hdcScreen == 0 {
		return nil, fmt.Errorf("GetDC screen failed: %v", errn)
	}
	defer procReleaseDC.Call(0, hdcScreen)

	x, _, _ := procGetSystemMetrics.Call(SM_XVIRTUALSCREEN)
	y, _, _ := procGetSystemMetrics.Call(SM_YVIRTUALSCREEN)
	w, _, _ := procGetSystemMetrics.Call(SM_CXVIRTUALSCREEN)
	h, _, _ := procGetSystemMetrics.Call(SM_CYVIRTUALSCREEN)
	if w == 0 || h == 0 {
		w, _, _ = procGetSystemMetrics.Call(SM_CXSCREEN)
		h, _, _ = procGetSystemMetrics.Call(SM_CYSCREEN)
		x, y = 0, 0
	}
	return blitScreen(hdcScreen, int32(x), int32(y), int32(w), int32(h))
}

func captureRectFromScreen(hwnd syscall.Handle) (image.Image, error) {
	left, top, width, height, err := visibleWindowRect(hwnd)
	if err != nil {
		return nil, err
	}
	hdcScreen, _, errn := procGetDC.Call(0)
	if hdcScreen == 0 {
		return nil, fmt.Errorf("GetDC failed: %v", errn)
	}
	defer procReleaseDC.Call(0, hdcScreen)
	return blitScreen(hdcScreen, left, top, width, height)
}

func capturePrintWindow(hwnd syscall.Handle) (image.Image, error) {
	var rect RECT
	ret, _, errn := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		return nil, fmt.Errorf("GetWindowRect failed: %v", errn)
	}
	w := rect.Right - rect.Left
	h := rect.Bottom - rect.Top
	if w < 2 || h < 2 {
		return nil, fmt.Errorf("invalid window size %dx%d", w, h)
	}
	if w > maxCaptureSide {
		w = maxCaptureSide
	}
	if h > maxCaptureSide {
		h = maxCaptureSide
	}

	hdcWin, _, errn := procGetDC.Call(uintptr(hwnd))
	if hdcWin == 0 {
		return nil, fmt.Errorf("GetDC window failed: %v", errn)
	}
	defer procReleaseDC.Call(uintptr(hwnd), hdcWin)

	bits, hbm, err := createDIB(hdcWin, w, h)
	if err != nil {
		return nil, err
	}
	defer procDeleteObject.Call(hbm)

	hdcMem, _, errn := procCreateCompatibleDC.Call(hdcWin)
	if hdcMem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed: %v", errn)
	}
	defer procDeleteDC.Call(hdcMem)
	old, _, _ := procSelectObject.Call(hdcMem, hbm)
	defer procSelectObject.Call(hdcMem, old)

	pRet, _, _ := procPrintWindow.Call(uintptr(hwnd), hdcMem, PW_RENDERFULLCONTENT)
	if pRet == 0 {
		pRet, _, errn = procPrintWindow.Call(uintptr(hwnd), hdcMem, 0)
		if pRet == 0 {
			return nil, fmt.Errorf("PrintWindow failed: %v", errn)
		}
	}
	return dibToImage(bits, w, h), nil
}

func blitScreen(hdcScreen uintptr, left, top, width, height int32) (image.Image, error) {
	if width < 2 || height < 2 {
		return nil, fmt.Errorf("invalid capture size %dx%d", width, height)
	}
	dstW, dstH := width, height
	if dstW > maxCaptureSide || dstH > maxCaptureSide {
		scale := float64(maxCaptureSide) / float64(dstW)
		if float64(maxCaptureSide)/float64(dstH) < scale {
			scale = float64(maxCaptureSide) / float64(dstH)
		}
		dstW = int32(float64(dstW) * scale)
		dstH = int32(float64(dstH) * scale)
		if dstW < 1 {
			dstW = 1
		}
		if dstH < 1 {
			dstH = 1
		}
	}

	bits, hbm, err := createDIB(hdcScreen, dstW, dstH)
	if err != nil {
		return nil, err
	}
	defer procDeleteObject.Call(hbm)

	hdcMem, _, errn := procCreateCompatibleDC.Call(hdcScreen)
	if hdcMem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed: %v", errn)
	}
	defer procDeleteDC.Call(hdcMem)
	old, _, _ := procSelectObject.Call(hdcMem, hbm)
	defer procSelectObject.Call(hdcMem, old)

	if dstW == width && dstH == height {
		ret, _, errn := procBitBlt.Call(hdcMem, 0, 0, uintptr(dstW), uintptr(dstH), hdcScreen, uintptr(left), uintptr(top), SRCCOPY)
		if ret == 0 {
			return nil, fmt.Errorf("BitBlt failed (%dx%d): %v", width, height, errn)
		}
	} else {
		ret, _, errn := procStretchBlt.Call(hdcMem, 0, 0, uintptr(dstW), uintptr(dstH), hdcScreen, uintptr(left), uintptr(top), uintptr(width), uintptr(height), SRCCOPY)
		if ret == 0 {
			return nil, fmt.Errorf("StretchBlt failed (%dx%d): %v", width, height, errn)
		}
	}
	return dibToImage(bits, dstW, dstH), nil
}

func createDIB(hdc uintptr, w, h int32) (unsafe.Pointer, uintptr, error) {
	var bi BITMAPINFO
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = w
	bi.BmiHeader.BiHeight = -h
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	bi.BmiHeader.BiCompression = BI_RGB

	var bits unsafe.Pointer
	hbm, _, errn := procCreateDIBSection.Call(
		hdc,
		uintptr(unsafe.Pointer(&bi)),
		DIB_RGB_COLORS,
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if hbm == 0 || bits == nil {
		return nil, 0, fmt.Errorf("CreateDIBSection failed (%dx%d): %v", w, h, errn)
	}
	return bits, hbm, nil
}

func dibToImage(bits unsafe.Pointer, w, h int32) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	stride := int(w) * 4
	src := unsafe.Slice((*byte)(bits), stride*int(h))
	for y := 0; y < int(h); y++ {
		row := src[y*stride : y*stride+stride]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < int(w); x++ {
			i := x * 4
			dst[i] = row[i+2]
			dst[i+1] = row[i+1]
			dst[i+2] = row[i]
			dst[i+3] = 255
		}
	}
	return img
}

func visibleWindowRect(hwnd syscall.Handle) (left, top, width, height int32, err error) {
	var wr RECT
	ret, _, errn := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr)))
	if ret == 0 {
		return 0, 0, 0, 0, fmt.Errorf("GetWindowRect failed: %v", errn)
	}
	vx, _, _ := procGetSystemMetrics.Call(SM_XVIRTUALSCREEN)
	vy, _, _ := procGetSystemMetrics.Call(SM_YVIRTUALSCREEN)
	vw, _, _ := procGetSystemMetrics.Call(SM_CXVIRTUALSCREEN)
	vh, _, _ := procGetSystemMetrics.Call(SM_CYVIRTUALSCREEN)
	sl, st := int32(vx), int32(vy)
	sr, sb := sl+int32(vw), st+int32(vh)
	if vw == 0 || vh == 0 {
		sl, st = 0, 0
		sw, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
		sh, _, _ := procGetSystemMetrics.Call(SM_CYSCREEN)
		sr, sb = int32(sw), int32(sh)
	}
	l := wr.Left
	t := wr.Top
	r := wr.Right
	b := wr.Bottom
	if l < sl {
		l = sl
	}
	if t < st {
		t = st
	}
	if r > sr {
		r = sr
	}
	if b > sb {
		b = sb
	}
	if r-l < 2 || b-t < 2 {
		l, t, r, b = wr.Left, wr.Top, wr.Right, wr.Bottom
	}
	if r-l < 2 || b-t < 2 {
		return 0, 0, 0, 0, fmt.Errorf("invalid window size %dx%d", wr.Right-wr.Left, wr.Bottom-wr.Top)
	}
	return l, t, r - l, b - t, nil
}

func largestChild(parent syscall.Handle) syscall.Handle {
	var best syscall.Handle
	var bestArea int32
	cb := syscall.NewCallback(func(hwnd syscall.Handle, _ uintptr) uintptr {
		var rect RECT
		procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
		w := rect.Right - rect.Left
		h := rect.Bottom - rect.Top
		if w > 50 && h > 50 && w*h > bestArea {
			bestArea = w * h
			best = hwnd
		}
		return 1
	})
	procEnumChildWindows.Call(uintptr(parent), cb, 0)
	return best
}

func affinityNote(hwnd syscall.Handle) string {
	var aff uint32
	ret, _, _ := procGetWindowDisplayAffinity.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&aff)))
	if ret == 0 || aff == 0 {
		return ""
	}
	switch aff {
	case 0x1:
		return "窗口设置了 WDA_MONITOR 防截屏"
	case 0x11:
		return "窗口设置了 WDA_EXCLUDEFROMCAPTURE 防截屏"
	default:
		return fmt.Sprintf("窗口显示亲和性=0x%X", aff)
	}
}

type monitorInfo struct {
	CbSize    uint32
	RcMonitor RECT
	RcWork    RECT
	DwFlags   uint32
}

func isImageBlank(img image.Image) bool {
	if img == nil {
		return true
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return true
	}
	r0, g0, b0, _ := img.At(w/2, h/2).RGBA()
	r1, g1, b1, _ := img.At(w/4, h/4).RGBA()
	r2, g2, b2, _ := img.At(w*3/4, h*3/4).RGBA()
	return r0 == 0 && g0 == 0 && b0 == 0 && r1 == 0 && g1 == 0 && b1 == 0 && r2 == 0 && g2 == 0 && b2 == 0
}
