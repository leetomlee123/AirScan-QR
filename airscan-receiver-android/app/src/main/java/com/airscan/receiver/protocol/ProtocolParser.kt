package com.airscan.receiver.protocol

object ProtocolParser {

    /**
     * Parses a raw scanned string according to the AirScan-QR protocol:
     * TaskID | FileName | TotalFrames | CurrentIndex | DataPayload
     *
     * Example:
     * 8AF|sample.png|12|0|iVBORw0KGgoAAAANSUhEUgAA...
     */
    fun parseFrame(rawText: String): QrFrame? {
        if (rawText.isBlank()) return null

        val parts = rawText.split("\\|".toRegex(), limit = 5)
        if (parts.size < 5) return null

        val taskId = parts[0].trim()
        val fileName = parts[1].trim()
        val totalFrames = parts[2].trim().toIntOrNull() ?: return null
        val currentIndex = parts[3].trim().toIntOrNull() ?: return null
        val payload = parts[4]

        if (taskId.isEmpty() || fileName.isEmpty()) return null
        if (totalFrames <= 0) return null
        if (currentIndex < 0 || currentIndex >= totalFrames) return null

        return QrFrame(
            taskId = taskId,
            fileName = fileName,
            totalFrames = totalFrames,
            currentIndex = currentIndex,
            payload = payload
        )
    }

    /**
     * Concatenates all slices from index 0 to totalFrames - 1 in order,
     * then decodes the concatenated Base64 string into raw bytes.
     */
    fun assembleFile(chunks: Map<Int, String>, totalFrames: Int): ByteArray {
        require(totalFrames > 0) { "Total frames must be greater than 0" }
        require(chunks.size >= totalFrames) { "Incomplete frames: received ${chunks.size} of $totalFrames" }

        val sb = StringBuilder()
        for (i in 0 until totalFrames) {
            val chunk = chunks[i] ?: throw IllegalStateException("Missing frame index $i")
            sb.append(chunk)
        }

        return decodeBase64(sb.toString())
    }

    /**
     * Safe Base64 decoder that handles padding, whitespace, and works seamlessly
     * on both Android runtime and standard JVM unit tests.
     */
    fun decodeBase64(base64Str: String): ByteArray {
        val cleanStr = base64Str.replace("\n", "").replace("\r", "").replace(" ", "").trim()
        val padding = (4 - (cleanStr.length % 4)) % 4
        val normalized = if (padding > 0) cleanStr + "=".repeat(padding) else cleanStr

        return try {
            val clazz = Class.forName("android.util.Base64")
            val method = clazz.getMethod("decode", String::class.java, Int::class.javaPrimitiveType)
            try {
                // Try standard DEFAULT (0)
                method.invoke(null, normalized, 0) as ByteArray
            } catch (_: Throwable) {
                // Fallback to URL_SAFE (8)
                method.invoke(null, normalized, 8) as ByteArray
            }
        } catch (_: Throwable) {
            try {
                java.util.Base64.getDecoder().decode(normalized)
            } catch (_: Throwable) {
                java.util.Base64.getUrlDecoder().decode(normalized)
            }
        }
    }
}
