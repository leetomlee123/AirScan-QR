package com.airscan.receiver.protocol

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.net.URLDecoder
import java.nio.charset.StandardCharsets
import java.util.zip.GZIPInputStream

/**
 * AirScan 统一帧数据模型
 */
sealed class AirScanPacket {
    abstract val taskId: String
    abstract val fileName: String

    /**
     * 标准切片模式：TaskID | FileName | TotalFrames | CurrentIndex | DataPayload
     */
    data class StandardFrame(
        override val taskId: String,
        override val fileName: String,
        val totalFrames: Int,
        val currentIndex: Int,
        val payload: String
    ) : AirScanPacket()

    /**
     * 喷泉码无码率模式：TaskID | Seed | TotalBlocks | Base64(XorData) | Meta(URL_FileName:FileSize)
     */
    data class FountainFrame(
        override val taskId: String,
        override val fileName: String,
        val seed: Long,
        val totalBlocks: Int,
        val xorData: ByteArray,
        val fileSize: Int
    ) : AirScanPacket() {
        override fun equals(other: Any?): Boolean {
            if (this === other) return true
            if (javaClass != other?.javaClass) return false
            other as FountainFrame
            return taskId == other.taskId && seed == other.seed && totalBlocks == other.totalBlocks && xorData.contentEquals(other.xorData)
        }

        override fun hashCode(): Int {
            var result = taskId.hashCode()
            result = 31 * result + seed.hashCode()
            result = 31 * result + totalBlocks
            result = 31 * result + xorData.contentHashCode()
            return result
        }
    }
}

// 保持向下兼容别名
typealias QrFrame = AirScanPacket.StandardFrame

object ProtocolParser {

    /**
     * 自动嗅探并解析扫描到的二维码内容：
     * 1. 喷泉码模式：5 段且 p[4] 包含冒号 ":"（Meta = UrlFileName:FileSize）
     * 2. 标准流水线模式：5 段且 p[2], p[3] 为纯数字索引
     */
    fun parsePacket(rawText: String): AirScanPacket? {
        if (rawText.isBlank()) return null
        val parts = rawText.split("\\|".toRegex(), limit = 5)
        if (parts.size < 5) return null

        val p0 = parts[0].trim()
        val p1 = parts[1].trim()
        val p2 = parts[2].trim()
        val p3 = parts[3].trim()
        val p4 = parts[4].trim()

        if (p0.isEmpty()) return null

        // 判断是否为喷泉码格式：p0=tid, p1=seed(long), p2=total(int), p3=b64(xordata), p4=meta(name:size)
        val seed = p1.toLongOrNull()
        val fountainTotal = p2.toIntOrNull()
        if (seed != null && fountainTotal != null && p4.contains(":")) {
            val metaParts = p4.split(":", limit = 2)
            if (metaParts.size == 2) {
                val rawName = metaParts[0]
                val fName = try { URLDecoder.decode(rawName, "UTF-8") } catch (_: Exception) { rawName }
                val fSize = metaParts[1].toIntOrNull()
                if (fSize != null && fSize >= 0) {
                    val xorBytes = try { decodeBase64(p3) } catch (_: Exception) { null }
                    if (xorBytes != null && xorBytes.isNotEmpty()) {
                        return AirScanPacket.FountainFrame(
                            taskId = p0,
                            fileName = fName,
                            seed = seed,
                            totalBlocks = fountainTotal,
                            xorData = xorBytes,
                            fileSize = fSize
                        )
                    }
                }
            }
        }

        // 标准切片模式：p0=tid, p1=fileName, p2=total, p3=index, p4=data
        val total = p2.toIntOrNull() ?: return null
        val curIdx = p3.toIntOrNull() ?: return null
        if (p1.isEmpty() || total <= 0 || curIdx < 0 || curIdx >= total) return null

        return AirScanPacket.StandardFrame(
            taskId = p0,
            fileName = p1,
            totalFrames = total,
            currentIndex = curIdx,
            payload = p4
        )
    }

    // 兼容老调用
    fun parseFrame(rawText: String): QrFrame? {
        return parsePacket(rawText) as? QrFrame
    }

    /**
     * 标准切片模式：拼接全部 Base64 分片 -> Base64解码 -> 自动 Gzip 探测解压
     */
    fun assembleStandardFile(chunks: Map<Int, String>, totalFrames: Int): ByteArray {
        require(totalFrames > 0) { "Total frames must be greater than 0" }
        require(chunks.size >= totalFrames) { "Incomplete frames: received ${chunks.size} of $totalFrames" }

        val sb = StringBuilder()
        for (i in 0 until totalFrames) {
            val chunk = chunks[i] ?: throw IllegalStateException("Missing frame index $i")
            sb.append(chunk)
        }

        val rawBytes = decodeBase64(sb.toString())
        return decompressIfGzip(rawBytes)
    }

    // 兼容老方法签名
    fun assembleFile(chunks: Map<Int, String>, totalFrames: Int): ByteArray {
        return assembleStandardFile(chunks, totalFrames)
    }

    /**
     * 自动探测魔数并解压 Gzip（Magic: 0x1F, 0x8B）。
     * 若不是 Gzip 压缩流，则返回原数据，无缝兼容未压缩与压缩传输！
     */
    fun decompressIfGzip(data: ByteArray): ByteArray {
        if (data.size < 2) return data
        val b0 = data[0].toInt() and 0xFF
        val b1 = data[1].toInt() and 0xFF
        if (b0 == 0x1F && b1 == 0x8B) {
            try {
                ByteArrayInputStream(data).use { bais ->
                    GZIPInputStream(bais).use { gzis ->
                        val baos = ByteArrayOutputStream()
                        val buffer = ByteArray(4096)
                        var len: Int
                        while (gzis.read(buffer).also { len = it } > 0) {
                            baos.write(buffer, 0, len)
                        }
                        return baos.toByteArray()
                    }
                }
            } catch (e: Exception) {
                // 解压异常则优雅回退原数据
                return data
            }
        }
        return data
    }

    /**
     * 健壮 Base64 解码器，自动清洗换行、空格与补齐 '='
     */
    fun decodeBase64(base64Str: String): ByteArray {
        val cleanStr = base64Str.replace("\n", "").replace("\r", "").replace(" ", "").trim()
        val padding = (4 - (cleanStr.length % 4)) % 4
        val normalized = if (padding > 0) cleanStr + "=".repeat(padding) else cleanStr

        return try {
            val clazz = Class.forName("android.util.Base64")
            val method = clazz.getMethod("decode", String::class.java, Int::class.javaPrimitiveType)
            try {
                method.invoke(null, normalized, 0) as ByteArray
            } catch (_: Throwable) {
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
