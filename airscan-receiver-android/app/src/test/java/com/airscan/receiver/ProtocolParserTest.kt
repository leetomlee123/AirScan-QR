package com.airscan.receiver

import com.airscan.receiver.protocol.*
import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.nio.charset.StandardCharsets
import java.util.zip.GZIPOutputStream

class ProtocolParserTest {

    @Test
    fun testParseValidStandardFrame() {
        val raw = "8AF|my_doc.pdf|10|0|JVBERi0xLjQKJ..."
        val packet = ProtocolParser.parsePacket(raw)

        assertNotNull(packet)
        assertTrue(packet is AirScanPacket.StandardFrame)
        val frame = packet as AirScanPacket.StandardFrame
        assertEquals("8AF", frame.taskId)
        assertEquals("my_doc.pdf", frame.fileName)
        assertEquals(10, frame.totalFrames)
        assertEquals(0, frame.currentIndex)
        assertEquals("JVBERi0xLjQKJ...", frame.payload)
    }

    @Test
    fun testParseValidFountainPacket() {
        // ID|seed|total|b64(xordata)|url_name:size
        val xorRaw = "TEST_XOR_PAYLOAD".toByteArray(StandardCharsets.UTF_8)
        val b64Xor = java.util.Base64.getEncoder().encodeToString(xorRaw)
        val raw = "F01|98765|4|$b64Xor|my%20file.bin:12345"

        val packet = ProtocolParser.parsePacket(raw)
        assertNotNull(packet)
        assertTrue(packet is AirScanPacket.FountainFrame)
        val fFrame = packet as AirScanPacket.FountainFrame
        assertEquals("F01", fFrame.taskId)
        assertEquals("my file.bin", fFrame.fileName)
        assertEquals(98765L, fFrame.seed)
        assertEquals(4, fFrame.totalBlocks)
        assertEquals(12345, fFrame.fileSize)
        assertArrayEquals(xorRaw, fFrame.xorData)
    }

    @Test
    fun testGzipAutomaticDecompression() {
        val originalText = "AirScan-QR High Speed Gzip Compression and Decompression Test String! 0123456789"
        val originalBytes = originalText.toByteArray(StandardCharsets.UTF_8)

        // 模拟发送端 Gzip 压缩
        val baos = ByteArrayOutputStream()
        GZIPOutputStream(baos).use { gzos ->
            gzos.write(originalBytes)
            gzos.flush()
        }
        val compressedBytes = baos.toByteArray()

        // 校验 decompressIfGzip 自动探测魔数并解压
        val decompressedBytes = ProtocolParser.decompressIfGzip(compressedBytes)
        assertEquals(originalText, String(decompressedBytes, StandardCharsets.UTF_8))

        // 普通未压缩流传入不会报错，保持原样
        val uncompressed = ProtocolParser.decompressIfGzip(originalBytes)
        assertArrayEquals(originalBytes, uncompressed)
    }

    @Test
    fun testEndToEndChunkingAndAssemblyWithGzip() {
        val originalText = "AirScan-QR: CameraX and ML Kit native high-speed barcode pipeline test! 1234567890"
        val originalBytes = originalText.toByteArray(StandardCharsets.UTF_8)

        // Gzip 压缩后切片
        val baos = ByteArrayOutputStream()
        GZIPOutputStream(baos).use { it.write(originalBytes) }
        val gzippedBytes = baos.toByteArray()
        val fullBase64 = java.util.Base64.getEncoder().encodeToString(gzippedBytes)

        val chunkSize = 15
        val chunks = mutableListOf<String>()
        var i = 0
        while (i < fullBase64.length) {
            val end = (i + chunkSize).coerceAtMost(fullBase64.length)
            chunks.add(fullBase64.substring(i, end))
            i = end
        }

        val totalFrames = chunks.size
        val chunkMap = mutableMapOf<Int, String>()

        for (idx in chunks.indices) {
            val raw = "TSK|test.txt|$totalFrames|$idx|${chunks[idx]}"
            val packet = ProtocolParser.parsePacket(raw) as? AirScanPacket.StandardFrame
            assertNotNull(packet)
            chunkMap[packet!!.currentIndex] = packet.payload
        }

        val assembledBytes = ProtocolParser.assembleStandardFile(chunkMap, totalFrames)
        val assembledText = String(assembledBytes, StandardCharsets.UTF_8)
        assertEquals(originalText, assembledText)
    }

    @Test
    fun testFountainDecoderEndToEnd() {
        val originalText = "Fountain Codes LT-Codes Kotlin Native Implementation Verification 123456"
        val originalBytes = originalText.toByteArray(StandardCharsets.UTF_8)
        val totalSize = originalBytes.size
        val chunkSize = 16
        val chunks = mutableListOf<ByteArray>()

        var pos = 0
        while (pos < totalSize) {
            val chunk = ByteArray(chunkSize)
            val len = (totalSize - pos).coerceAtMost(chunkSize)
            System.arraycopy(originalBytes, pos, chunk, 0, len)
            chunks.add(chunk)
            pos += chunkSize
        }

        val totalBlocks = chunks.size
        val decoder = FountainDecoder("FT1", "test.txt", totalBlocks, totalSize, chunkSize)

        // 模拟喷泉发射水滴包
        var seed = 12345L
        var iterations = 0
        while (!decoder.isCompleted && iterations < 500) {
            seed += 7
            iterations++

            val rng = FountainDecoder.PRNG(seed)
            val p = rng.next()
            val d = when {
                p < 0.4 -> 1
                p < 0.7 -> 2
                p < 0.9 -> (rng.next() * 10).toInt() + 3
                else -> (rng.next() * totalBlocks * 0.5).toInt() + 2
            }.coerceAtMost(totalBlocks).coerceAtLeast(1)

            val idxSet = mutableSetOf<Int>()
            while (idxSet.size < d) {
                idxSet.add((rng.next() * totalBlocks).toInt().coerceIn(0, totalBlocks - 1))
            }

            val xorBuf = ByteArray(chunkSize)
            for (idx in idxSet) {
                FountainDecoder.xorBytes(xorBuf, chunks[idx])
            }

            decoder.processPacket(seed, xorBuf)
        }

        assertTrue("Fountain decoding should complete", decoder.isCompleted)
        val assembled = decoder.assemble()
        assertEquals(originalText, String(assembled, StandardCharsets.UTF_8))
    }

    @Test
    fun testTransferSessionManagerOutOfOrderAndDuplicates() {
        val session = TransferSessionManager()
        val originalText = "Fast Transfer Out Of Order Test"
        val b64 = java.util.Base64.getEncoder().encodeToString(originalText.toByteArray())

        val c0 = b64.substring(0, 10)
        val c1 = b64.substring(10, 20)
        val c2 = b64.substring(20)

        // 1. Scan chunk 1 first
        val f1 = AirScanPacket.StandardFrame("TS1", "sample.txt", 3, 1, c1)
        val state1 = session.onPacketScanned(f1)
        assertEquals(1, state1.receivedCount)
        assertFalse(state1.isCompleted)

        // 2. Scan chunk 0
        val f0 = AirScanPacket.StandardFrame("TS1", "sample.txt", 3, 0, c0)
        val state2 = session.onPacketScanned(f0)
        assertEquals(2, state2.receivedCount)

        // 3. Scan chunk 2 (completes)
        val f2 = AirScanPacket.StandardFrame("TS1", "sample.txt", 3, 2, c2)
        val state3 = session.onPacketScanned(f2)
        assertEquals(3, state3.receivedCount)
        assertTrue(state3.isCompleted)
        assertNotNull(state3.assembledData)
        assertEquals(originalText, String(state3.assembledData!!, StandardCharsets.UTF_8))

        // 4. Subsequent frames scanned after completion MUST retain assembledData!
        val state4 = session.onPacketScanned(f2)
        assertTrue(state4.isCompleted)
        assertNotNull(state4.assembledData)
        assertEquals(originalText, String(state4.assembledData!!, StandardCharsets.UTF_8))
    }
}
