package com.airscan.receiver

import com.airscan.receiver.protocol.ProtocolParser
import com.airscan.receiver.protocol.QrFrame
import com.airscan.receiver.protocol.TransferSessionManager
import org.junit.Assert.*
import org.junit.Test
import java.nio.charset.StandardCharsets

class ProtocolParserTest {

    @Test
    fun testParseValidFrame() {
        val raw = "8AF|my_doc.pdf|10|0|JVBERi0xLjQKJ..."
        val frame = ProtocolParser.parseFrame(raw)

        assertNotNull(frame)
        assertEquals("8AF", frame?.taskId)
        assertEquals("my_doc.pdf", frame?.fileName)
        assertEquals(10, frame?.totalFrames)
        assertEquals(0, frame?.currentIndex)
        assertEquals("JVBERi0xLjQKJ...", frame?.payload)
    }

    @Test
    fun testParseLastFrameIndex() {
        val raw = "ABC|archive.zip|5|4|UEsDBBQAA..."
        val frame = ProtocolParser.parseFrame(raw)

        assertNotNull(frame)
        assertEquals("ABC", frame?.taskId)
        assertEquals("archive.zip", frame?.fileName)
        assertEquals(5, frame?.totalFrames)
        assertEquals(4, frame?.currentIndex)
        assertEquals("UEsDBBQAA...", frame?.payload)
    }

    @Test
    fun testParseInvalidFormats() {
        // Less than 5 segments
        assertNull(ProtocolParser.parseFrame("ABC|test.txt|5|0"))
        // Blank
        assertNull(ProtocolParser.parseFrame(""))
        assertNull(ProtocolParser.parseFrame("   "))
        // Non-number total
        assertNull(ProtocolParser.parseFrame("ABC|test.txt|ten|0|data"))
        // Non-number index
        assertNull(ProtocolParser.parseFrame("ABC|test.txt|10|first|data"))
        // Index >= Total
        assertNull(ProtocolParser.parseFrame("ABC|test.txt|5|5|data"))
        assertNull(ProtocolParser.parseFrame("ABC|test.txt|5|6|data"))
        // Negative index
        assertNull(ProtocolParser.parseFrame("ABC|test.txt|5|-1|data"))
        // Zero total
        assertNull(ProtocolParser.parseFrame("ABC|test.txt|0|0|data"))
    }

    @Test
    fun testEndToEndChunkingAndAssembly() {
        val originalText = "AirScan-QR: CameraX and ML Kit native high-speed barcode pipeline test! 1234567890"
        val originalBytes = originalText.toByteArray(StandardCharsets.UTF_8)
        val fullBase64 = java.util.Base64.getEncoder().encodeToString(originalBytes)

        // Split into chunks of 15 characters
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
            val frame = ProtocolParser.parseFrame(raw)
            assertNotNull(frame)
            chunkMap[frame!!.currentIndex] = frame.payload
        }

        val assembledBytes = ProtocolParser.assembleFile(chunkMap, totalFrames)
        val assembledText = String(assembledBytes, StandardCharsets.UTF_8)

        assertEquals(originalText, assembledText)
    }

    @Test
    fun testTransferSessionManagerOutOfOrderAndDuplicates() {
        val session = TransferSessionManager()
        val originalText = "Fast Transfer Out Of Order Test"
        val b64 = java.util.Base64.getEncoder().encodeToString(originalText.toByteArray())

        // 3 chunks
        val c0 = b64.substring(0, 10)
        val c1 = b64.substring(10, 20)
        val c2 = b64.substring(20)

        // 1. Scan chunk 1 first (out of order)
        val f1 = QrFrame("TS1", "sample.txt", 3, 1, c1)
        val state1 = session.onFrameScanned(f1)
        assertEquals(1, state1.receivedCount)
        assertFalse(state1.isCompleted)
        assertEquals(listOf(1, 3), state1.missingFrames) // 1-based: frame 1 and 3 missing

        // 2. Scan chunk 1 again (duplicate from high camera FPS)
        val state1Dup = session.onFrameScanned(f1)
        assertEquals(1, state1Dup.receivedCount)

        // 3. Scan chunk 0
        val f0 = QrFrame("TS1", "sample.txt", 3, 0, c0)
        val state2 = session.onFrameScanned(f0)
        assertEquals(2, state2.receivedCount)
        assertEquals(listOf(3), state2.missingFrames) // Only frame 3 (0-index 2) missing

        // 4. Scan chunk 2 (completes)
        val f2 = QrFrame("TS1", "sample.txt", 3, 2, c2)
        val state3 = session.onFrameScanned(f2)
        assertEquals(3, state3.receivedCount)
        assertTrue(state3.isCompleted)
        assertTrue(state3.missingFrames.isEmpty())
        assertNotNull(state3.assembledData)
        assertEquals(originalText, String(state3.assembledData!!, StandardCharsets.UTF_8))
    }

    @Test
    fun testTaskIdSwitchResetsSession() {
        val session = TransferSessionManager()

        val f1 = QrFrame("TASK_A", "a.txt", 5, 0, "AAA")
        val state1 = session.onFrameScanned(f1)
        assertEquals("TASK_A", state1.taskId)
        assertEquals(1, state1.receivedCount)

        // Switch to new Task ID
        val f2 = QrFrame("TASK_B", "b.txt", 2, 0, "BBB")
        val state2 = session.onFrameScanned(f2)
        assertEquals("TASK_B", state2.taskId)
        assertEquals("b.txt", state2.fileName)
        assertEquals(1, state2.receivedCount)
        assertEquals(2, state2.totalFrames)
    }
}
