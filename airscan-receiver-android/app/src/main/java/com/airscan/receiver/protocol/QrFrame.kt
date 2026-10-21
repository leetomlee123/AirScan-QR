package com.airscan.receiver.protocol

/**
 * Represents a single QR code frame parsed from the AirScan-QR protocol stream:
 * TaskID | FileName | TotalFrames | CurrentIndex | DataPayload
 */
data class QrFrame(
    val taskId: String,
    val fileName: String,
    val totalFrames: Int,
    val currentIndex: Int,
    val payload: String
)
