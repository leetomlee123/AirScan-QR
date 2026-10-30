# AirScan-QR 专用 Windows 原生接收端

专为解决网页版 `getDisplayMedia` **无法枚举/看不见特定应用（如企业远程桌面、Citrix、部分受保护窗口或后台应用）** 而重构的 Windows 原生桌面接收端。

### 🌟 特性优势

1. **彻底解决"看不见 app"**：通过 Windows 底层 Win32 GDI / `PrintWindow` API，直接穿透枚举系统所有真实窗口（包含后台窗口、被遮挡窗口）。
2. **超轻单文件**：纯静态编译，单文件 `.exe` 仅 **7MB**，无需安装任何依赖（不需要 Python、Node.js、WebView2 运行库），双击即用。
3. **双协议全支持**：
   - 支持 [index.html](../index.html) 的**单码循环广播**协议
   - 支持 [airscan-fountain.html](../airscan-fountain.html) 的**四码喷泉码（LT-Codes）**极速多象限识别协议
4. **自动组包与落盘**：文件接收重组校验完毕后，自动保存到当前用户的【下载（Downloads）】文件夹。

---

### 🚀 运行方法

1. 双击运行 `airscan-receiver-win.exe`。
2. 程序会自动在默认浏览器中打开控制面板（`http://127.0.0.1:端口`）。
3. 在下拉菜单中选择你要捕获的目标应用窗口（或选择 `🖥️ [全屏捕获]`）。
4. 点击 **"开始捕获并扫码"**，即可开始接收传输数据。

---

### ⚠️ 已知困境：Windows 远程桌面（RDP）防截屏保护

#### 问题现象

在以下场景中，使用本程序的屏幕捕获功能时，所有窗口抓取均返回**纯黑画面**，导致无法扫码：

- **微软 RDP 远程桌面**（mstsc）连接到启用了"屏幕捕获保护"策略的企业服务器
- **Citrix Workspace / VMware Horizon** 等虚拟桌面客户端（开启了 DLP 防泄漏模式）
- **中兴云桌面 / 移动云桌面** 等国产企业云桌面（uSmartView.exe 等）
- 其他任何调用了 `SetWindowDisplayAffinity(WDA_MONITOR / WDA_EXCLUDEFROMCAPTURE)` 的受保护客户端

#### 根本原因

Windows 的防截屏保护在 **DWM（桌面窗口管理器）内核层**实现，受保护区域的像素在合成到显卡输出之前已被替换为纯黑。本程序已实现的四种捕获策略全部处于 DWM 下游的**用户态**，因此均无法突破：

| 捕获方式 | 技术 | RDP 防截屏下的结果 |
|---|---|---|
| `wgcCaptureWindow` | Windows Graphics Capture (WGC) | ❌ 返回黑帧 |
| `captureRectFromScreen` | GDI `BitBlt` 全屏裁切 | ❌ 返回黑帧 |
| `capturePrintWindow` | `PrintWindow(PW_RENDERFULLCONTENT)` | ❌ 返回黑帧 |
| `wgcCaptureMonitorCrop` | WGC 捕获整块显示器后裁切 | ❌ 返回黑帧 |

若要从用户态彻底突破，理论上需要实现**内核态显示驱动（WDDM miniport）级别的 hook**，在企业管控环境（含 EDR/反病毒）中此路基本不可行。

#### 当前可用的替代方案

**方案一：用手机物理扫码（立即可用，无需任何改动）**

软件防截屏只能拦截操作系统内部的像素读取，**无法阻止显示器向外发出的光**。

1. 在远程桌面内打开 AirScan-QR 发送端（`index.html` 或 `airscan_cli.py`）。
2. 用手机摄像头对准物理显示器扫码，接收文件。
3. 若最终需要文件落到本地 PC，可通过 USB / 局域网从手机导出。

**方案二：在本程序的 Web 控制台里接入本机摄像头（待实现）**

在 `web/index.html` 增加"📷 摄像头扫码"模式，通过浏览器 `getUserMedia` API 调用本机内置摄像头或 USB 摄像头，用 jsQR / ZXing-js 在前端完成实时解码，解码结果上报给 Go 引擎完成文件组装和落盘。

> 此方案**不修改 Go 代码**，仅修改内嵌 HTML，可同时保留现有的屏幕捕获模式。欢迎 PR！

**方案三：在发送侧改用网络传输（绕过视觉链路）**

若远程桌面的网络策略允许 TCP 出站（如内网穿透、反向代理），可在远程 PC 启动一个临时 HTTP 服务（`python -m http.server`），在本地 PC 直接通过浏览器下载文件，彻底绕开"屏幕→摄像头"的视觉链路。

---

### 🛠️ 重新编译说明

若需修改源码并重新编译：

```bash
cd airscan-receiver-win
# 编译为 Windows 64 位单文件可执行程序
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o airscan-receiver-win.exe .
```
