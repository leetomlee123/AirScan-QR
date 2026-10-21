# AirScan-QR 原生 Android 接收端 📱

基于 **CameraX + Google ML Kit** 原生条码扫描构建的高性能、低延迟 AirScan-QR 接收端。

## 🌟 为什么选择原生实现

与浏览器网页端（`getUserMedia` + JS `jsQR`/`html5-qrcode`）相比：
1. **更高帧率与更低延迟**：直接运行在 Android 原生相机流水线（30 ~ 60 FPS），告别网页版卡顿与跳帧。
2. **硬件加速**：充分利用移动端 GPU 与 NPU 加速二维码检测，CPU 占用极低，发热量显著减少。
3. **高帧率去重与乱序重组**：针对流水线协议支持乱序到达自动组包、毫秒级去重与缺失帧实时可视化。
4. **原生文件存储**：支持 Android SAF (Storage Access Framework)，无需申请高危存储权限即可直接保存到“下载”目录或任意文件夹。

## 🛠️ 技术栈

- **Language**: Kotlin
- **Camera Pipeline**: AndroidX CameraX (`ImageAnalysis` + `PreviewView`)
- **QR Recognition**: Google ML Kit (`com.google.mlkit:barcode-scanning`)
- **UI Framework**: Jetpack Compose + Material 3
- **Architecture**: MVVM + StateFlow + Kotlin Coroutines
- **CI / Build**: GitHub Actions + Gradle 8.7 + AGP 8.4

## 📦 快速下载与使用

1. 每次提交代码或触发 GitHub Actions，CI 会自动运行单元测试并构建出最新的 `app-debug.apk`。
2. 在 GitHub 仓库页面的 **Actions -> Android CI & Build** 中，即可下载 `airscan-receiver-debug-apk` 产物安装体验。

## 💻 本地构建

```bash
cd airscan-receiver-android

# 运行单元测试
./gradlew testDebugUnitTest

# 构建 Debug APK
./gradlew assembleDebug
```
构建产物位于 `app/build/outputs/apk/debug/app-debug.apk`。
