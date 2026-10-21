package com.airscan.receiver.camera

import androidx.annotation.OptIn
import androidx.camera.core.ExperimentalGetImage
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import com.airscan.receiver.protocol.AirScanPacket
import com.airscan.receiver.protocol.ProtocolParser
import com.google.mlkit.vision.barcode.BarcodeScannerOptions
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage

class QrCodeAnalyzer(
    private val onPacketFound: (AirScanPacket) -> Unit
) : ImageAnalysis.Analyzer {

    // 限制仅识别 QR_CODE 格式，以取得最高 FPS 与最低 CPU 损耗
    private val scanner = BarcodeScanning.getClient(
        BarcodeScannerOptions.Builder()
            .setBarcodeFormats(Barcode.FORMAT_QR_CODE)
            .build()
    )

    @OptIn(ExperimentalGetImage::class)
    override fun analyze(imageProxy: ImageProxy) {
        val mediaImage = imageProxy.image
        if (mediaImage != null) {
            val inputImage = InputImage.fromMediaImage(
                mediaImage,
                imageProxy.imageInfo.rotationDegrees
            )

            scanner.process(inputImage)
                .addOnSuccessListener { barcodes ->
                    // 原生支持同时捕捉画面中的多个二维码（适配喷泉码 4 码同屏并发）
                    for (barcode in barcodes) {
                        val rawValue = barcode.rawValue ?: continue
                        val packet = ProtocolParser.parsePacket(rawValue)
                        if (packet != null) {
                            onPacketFound(packet)
                        }
                    }
                }
                .addOnFailureListener {
                    // 忽略帧间瞬态错误
                }
                .addOnCompleteListener {
                    imageProxy.close()
                }
        } else {
            imageProxy.close()
        }
    }
}
