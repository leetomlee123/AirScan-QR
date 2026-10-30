//go:build !windows

package main

import (
	"fmt"
	"image"
)

type WindowInfo struct {
	Handle      uint64 `json:"handle"`
	Title       string `json:"title"`
	ProcessName string `json:"processName"`
	ClassName   string `json:"className"`
	Width       int32  `json:"width"`
	Height      int32  `json:"height"`
	Affinity    uint32 `json:"affinity,omitempty"`
}

func initDPI() {}

func ListAllWindows() ([]WindowInfo, error) {
	return []WindowInfo{
		{Handle: 1, Title: "[ZTE] 中兴云桌面 (模拟)", ProcessName: "uSmartView.exe", ClassName: "DirectUIHWND", Width: 1920, Height: 1080},
	}, nil
}

func CaptureWindow(hwnd uint64) (image.Image, error) {
	return nil, fmt.Errorf("CaptureWindow is only supported on Windows")
}

func CaptureScreen() (image.Image, error) {
	return nil, fmt.Errorf("CaptureScreen is only supported on Windows")
}
