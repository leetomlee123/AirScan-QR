package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	tuotoo_qr "github.com/tuotoo/qrcode"
)

type FountainPRNG struct {
	seed int64
}

func NewPRNG(seed int64) *FountainPRNG {
	return &FountainPRNG{seed: seed}
}

func (p *FountainPRNG) Next() float64 {
	p.seed = (p.seed * 16807) % 2147483647
	return float64(p.seed-1) / 2147483646.0
}

func getDegree(L int, rng *FountainPRNG) int {
	prob := rng.Next()
	if prob < 0.4 {
		return 1
	}
	if prob < 0.7 {
		return 2
	}
	if prob < 0.9 {
		deg := int(rng.Next()*10) + 3
		if deg > L {
			return L
		}
		return deg
	}
	deg := int(rng.Next()*float64(L)*0.5) + 2
	if deg > L {
		return L
	}
	return deg
}

func xorBytes(dst, src []byte) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	for i := 0; i < n; i++ {
		dst[i] ^= src[i]
	}
}

type QueueItem struct {
	idxs []int
	data []byte
}

type ReceiverEngine struct {
	mu           sync.Mutex
	active       bool
	targetHandle uint64

	// 实时调试与画面预览
	lastPreviewJpeg []byte
	captureCount    int
	lastError       string

	// 协议状态
	mode           string // "standard" or "fountain"
	taskID         string
	fileName       string
	fileSize       int
	total          int
	currentChunkSz int
	startTime      time.Time
	isFinishing    bool
	progressPct    int
	statusText     string
	lastSavedPath  string

	// 标准切片模式
	standardChunks map[int]string

	// 喷泉码模式
	fountainSolved map[int][]byte
	fountainQueue  []QueueItem
}

func NewReceiverEngine() *ReceiverEngine {
	return &ReceiverEngine{
		standardChunks: make(map[int]string),
		fountainSolved: make(map[int][]byte),
		statusText:     "空闲",
	}
}

type StatusResp struct {
	Active        bool   `json:"active"`
	Mode          string `json:"mode"`
	TaskID        string `json:"taskId"`
	FileName      string `json:"fileName"`
	FileSize      int    `json:"fileSize"`
	Total         int    `json:"total"`
	SolvedCount   int    `json:"solvedCount"`
	ProgressPct   int    `json:"progressPct"`
	StatusText    string `json:"statusText"`
	LastSavedPath string `json:"lastSavedPath"`
	CaptureCount  int    `json:"captureCount"`
	LastError     string `json:"lastError"`
}

func (r *ReceiverEngine) GetStatus() StatusResp {
	r.mu.Lock()
	defer r.mu.Unlock()

	solved := 0
	if r.mode == "standard" {
		solved = len(r.standardChunks)
	} else if r.mode == "fountain" {
		solved = len(r.fountainSolved)
	}

	return StatusResp{
		Active:        r.active,
		Mode:          r.mode,
		TaskID:        r.taskID,
		FileName:      r.fileName,
		FileSize:      r.fileSize,
		Total:         r.total,
		SolvedCount:   solved,
		ProgressPct:   r.progressPct,
		StatusText:    r.statusText,
		LastSavedPath: r.lastSavedPath,
		CaptureCount:  r.captureCount,
		LastError:     r.lastError,
	}
}

func (r *ReceiverEngine) GetPreview() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastPreviewJpeg
}

func (r *ReceiverEngine) StartCapture(handle uint64) {
	r.mu.Lock()
	r.active = true
	r.targetHandle = handle
	r.statusText = "正在扫描画面..."
	r.captureCount = 0
	r.lastError = ""
	r.mu.Unlock()

	go r.scanLoop()
}

func (r *ReceiverEngine) StopCapture() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = false
	r.statusText = "已停止"
}

func (r *ReceiverEngine) scanLoop() {
	ticker := time.NewTicker(50 * time.Millisecond) // ~20 FPS 扫描
	defer ticker.Stop()

	previewTicker := time.NewTicker(200 * time.Millisecond) // 预览更新
	defer previewTicker.Stop()

	for {
		select {
		case <-ticker.C:
			r.mu.Lock()
			if !r.active {
				r.mu.Unlock()
				return
			}
			h := r.targetHandle
			r.mu.Unlock()

			var img image.Image
			var err error
			if h == 0 {
				img, err = CaptureScreen()
			} else {
				img, err = CaptureWindow(h)
			}

			if img == nil {
				r.mu.Lock()
				if err != nil {
					r.lastError = err.Error()
				}
				r.mu.Unlock()
				time.Sleep(100 * time.Millisecond)
				continue
			}

			r.mu.Lock()
			r.captureCount++
			if err != nil {
				r.lastError = err.Error()
			} else {
				r.lastError = ""
			}
			r.mu.Unlock()

			// 执行双引擎扫码
			r.scanImage(img)

		case <-previewTicker.C:
			r.mu.Lock()
			if !r.active {
				r.mu.Unlock()
				return
			}
			h := r.targetHandle
			r.mu.Unlock()

			var img image.Image
			if h == 0 {
				img, _ = CaptureScreen()
			} else {
				img, _ = CaptureWindow(h)
			}
			if img != nil {
				var buf bytes.Buffer
				if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 50}); err == nil {
					r.mu.Lock()
					r.lastPreviewJpeg = buf.Bytes()
					r.mu.Unlock()
				}
			}
		}
	}
}

func (r *ReceiverEngine) scanImage(img image.Image) {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w < 50 || h < 50 {
		return
	}

	type SubRect struct {
		x0, y0, x1, y1 int
	}

	rects := []SubRect{
		{0, 0, w, h},                 // 全屏
		{w / 4, h / 4, w * 3 / 4, h * 3 / 4}, // 画面中心
		{0, 0, w / 2, h / 2},         // 左上
		{w / 2, 0, w, h / 2},         // 右上
		{0, h / 2, w / 2, h},         // 左下
		{w / 2, h / 2, w, h},         // 右下
	}

	for _, sub := range rects {
		subImg := cropImage(img, sub.x0, sub.y0, sub.x1, sub.y1)

		// 引擎 1: gozxing 解码器
		bmp, err := gozxing.NewBinaryBitmapFromImage(subImg)
		if err == nil && bmp != nil {
			reader := qrcode.NewQRCodeReader()
			hints := map[gozxing.DecodeHintType]interface{}{
				gozxing.DecodeHintType_TRY_HARDER: true,
			}
			res, err := reader.Decode(bmp, hints)
			if err == nil && res != nil {
				r.handleDecoded(res.GetText())
				continue
			}
		}

		// 引擎 2: tuotoo qrcode 解码器作为高召回双保底
		var imgBuf bytes.Buffer
		if err := jpeg.Encode(&imgBuf, subImg, &jpeg.Options{Quality: 90}); err == nil {
			matrix, err := tuotoo_qr.Decode(bytes.NewReader(imgBuf.Bytes()))
			if err == nil && matrix != nil && matrix.Content != "" {
				r.handleDecoded(matrix.Content)
			}
		}
	}
}

func cropImage(src image.Image, x0, y0, x1, y1 int) image.Image {
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := src.(subImager); ok {
		return si.SubImage(image.Rect(x0, y0, x1, y1))
	}
	return src
}

func (r *ReceiverEngine) handleDecoded(raw string) {
	p := strings.Split(raw, "|")
	if len(p) < 5 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 区分喷泉码与标准码
	if strings.Contains(p[4], ":") && !strings.Contains(p[1], ".") {
		r.handleFountain(p)
	} else {
		r.handleStandard(p)
	}
}

// 1. 标准单码模式解码
func (r *ReceiverEngine) handleStandard(p []string) {
	tid := p[0]
	fileName := p[1]
	total, err1 := strconv.Atoi(p[2])
	idx, err2 := strconv.Atoi(p[3])
	dataB64 := p[4]
	if err1 != nil || err2 != nil || total <= 0 {
		return
	}

	if r.taskID != tid {
		r.mode = "standard"
		r.taskID = tid
		r.fileName = fileName
		r.total = total
		r.standardChunks = make(map[int]string)
		r.startTime = time.Now()
		r.isFinishing = false
		r.statusText = fmt.Sprintf("接收中: %s", fileName)
	}

	if _, exists := r.standardChunks[idx]; !exists {
		r.standardChunks[idx] = dataB64
	}

	count := len(r.standardChunks)
	r.progressPct = int(float64(count) / float64(total) * 100)
	r.statusText = fmt.Sprintf("接收中: %d / %d 帧 (%d%%)", count, total, r.progressPct)

	if count >= total && !r.isFinishing {
		r.isFinishing = true
		r.saveStandardFile()
	}
}

func (r *ReceiverEngine) saveStandardFile() {
	var fullB64 strings.Builder
	for i := 0; i < r.total; i++ {
		fullB64.WriteString(r.standardChunks[i])
	}

	rawBytes, err := base64.StdEncoding.DecodeString(fullB64.String())
	if err != nil {
		r.statusText = "Base64 解码失败: " + err.Error()
		log.Println("Decode failed:", err)
		return
	}

	savePath := filepath.Join(getDownloadsDir(), r.fileName)
	err = os.WriteFile(savePath, rawBytes, 0644)
	if err != nil {
		r.statusText = "保存失败: " + err.Error()
		log.Println("Save failed:", err)
		return
	}

	r.lastSavedPath = savePath
	r.statusText = fmt.Sprintf("✅ 接收成功！已保存至: %s", savePath)
	log.Println("File saved successfully:", savePath)
}

// 2. 喷泉码模式解码
func (r *ReceiverEngine) handleFountain(p []string) {
	tid := p[0]
	seed, err1 := strconv.ParseInt(p[1], 10, 64)
	total, err2 := strconv.Atoi(p[2])
	b64Data := p[3]
	meta := p[4]
	if err1 != nil || err2 != nil || total <= 0 {
		return
	}

	metaParts := strings.Split(meta, ":")
	if len(metaParts) < 2 {
		return
	}
	fName, _ := url.QueryUnescape(metaParts[0])
	fSize, _ := strconv.Atoi(metaParts[1])

	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return
	}

	if r.taskID != tid {
		r.mode = "fountain"
		r.taskID = tid
		r.total = total
		r.fountainSolved = make(map[int][]byte)
		r.fountainQueue = make([]QueueItem, 0)
		r.fileName = fName
		r.fileSize = fSize
		r.currentChunkSz = len(data)
		r.startTime = time.Now()
		r.isFinishing = false
		r.statusText = fmt.Sprintf("喷泉码接收中: %s", fName)
	}

	if len(r.fountainSolved) < r.total {
		r.processFountainPacket(seed, total, data)
	}
}

func (r *ReceiverEngine) processFountainPacket(seed int64, L int, data []byte) {
	rng := NewPRNG(seed)
	d := getDegree(L, rng)

	idxSet := make(map[int]bool)
	var idxs []int
	for len(idxs) < d {
		x := int(rng.Next() * float64(L))
		if !idxSet[x] {
			idxSet[x] = true
			idxs = append(idxs, x)
		}
	}

	if L == 1 {
		if _, exists := r.fountainSolved[0]; !exists {
			r.fountainSolved[0] = data
		}
		r.updateFountainProgress()
		return
	}

	sort.Ints(idxs)
	curData := make([]byte, len(data))
	copy(curData, data)

	// 消元已有块
	var remainingIdxs []int
	for _, idx := range idxs {
		if solved, ok := r.fountainSolved[idx]; ok {
			xorBytes(curData, solved)
		} else {
			remainingIdxs = append(remainingIdxs, idx)
		}
	}

	if len(remainingIdxs) == 0 {
		return
	}

	for _, item := range r.fountainQueue {
		if len(remainingIdxs) == 0 {
			break
		}
		if len(item.idxs) > 0 && item.idxs[0] == remainingIdxs[0] {
			xorBytes(curData, item.data)
			remainingIdxs = xorIdxLists(remainingIdxs, item.idxs)
		}
	}

	if len(remainingIdxs) > 0 {
		r.fountainQueue = append(r.fountainQueue, QueueItem{idxs: remainingIdxs, data: curData})
		sort.Slice(r.fountainQueue, func(i, j int) bool {
			return r.fountainQueue[i].idxs[0] < r.fountainQueue[j].idxs[0]
		})
	}

	// 回代求解
	foundNew := true
	for foundNew {
		foundNew = false
		for i := 0; i < len(r.fountainQueue); i++ {
			if len(r.fountainQueue[i].idxs) == 1 {
				sIdx := r.fountainQueue[i].idxs[0]
				sData := r.fountainQueue[i].data
				r.fountainSolved[sIdx] = sData
				r.fountainQueue = append(r.fountainQueue[:i], r.fountainQueue[i+1:]...)
				foundNew = true

				for j := range r.fountainQueue {
					if contains(r.fountainQueue[j].idxs, sIdx) {
						xorBytes(r.fountainQueue[j].data, sData)
						r.fountainQueue[j].idxs = removeIdx(r.fountainQueue[j].idxs, sIdx)
					}
				}
				break
			}
		}
	}

	r.updateFountainProgress()
}

func xorIdxLists(a, b []int) []int {
	m := make(map[int]bool)
	for _, v := range a {
		m[v] = true
	}
	for _, v := range b {
		if m[v] {
			delete(m, v)
		} else {
			m[v] = true
		}
	}
	res := make([]int, 0, len(m))
	for k := range m {
		res = append(res, k)
	}
	sort.Ints(res)
	return res
}

func contains(arr []int, target int) bool {
	for _, v := range arr {
		if v == target {
			return true
		}
	}
	return false
}

func removeIdx(arr []int, target int) []int {
	res := make([]int, 0, len(arr))
	for _, v := range arr {
		if v != target {
			res = append(res, v)
		}
	}
	return res
}

func (r *ReceiverEngine) updateFountainProgress() {
	done := len(r.fountainSolved)
	if r.total > 0 {
		r.progressPct = int(float64(done) / float64(r.total) * 100)
		if r.progressPct > 100 {
			r.progressPct = 100
		}
	}
	r.statusText = fmt.Sprintf("喷泉码解码中: %d / %d (%d%%)", done, r.total, r.progressPct)

	if done >= r.total && !r.isFinishing {
		r.isFinishing = true
		r.saveFountainFile()
	}
}

func (r *ReceiverEngine) saveFountainFile() {
	finalBuf := make([]byte, r.fileSize)
	for i := 0; i < r.total; i++ {
		start := i * r.currentChunkSz
		if start < r.fileSize {
			l := r.currentChunkSz
			if r.fileSize-start < l {
				l = r.fileSize - start
			}
			if chunk, ok := r.fountainSolved[i]; ok {
				copy(finalBuf[start:start+l], chunk[:l])
			}
		}
	}

	savePath := filepath.Join(getDownloadsDir(), r.fileName)
	err := os.WriteFile(savePath, finalBuf, 0644)
	if err != nil {
		r.statusText = "保存失败: " + err.Error()
		log.Println("Save failed:", err)
		return
	}

	r.lastSavedPath = savePath
	r.statusText = fmt.Sprintf("✅ 喷泉码接收成功！已保存至: %s", savePath)
	log.Println("Fountain file saved successfully:", savePath)
}

func getDownloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	dl := filepath.Join(home, "Downloads")
	if _, err := os.Stat(dl); err == nil {
		return dl
	}
	return "."
}
