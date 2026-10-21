package com.airscan.receiver.protocol

enum class TransferMode {
    STANDARD,  // 标准流水线切片模式
    FOUNTAIN   // 无码率喷泉码模式
}

data class TransferState(
    val mode: TransferMode = TransferMode.STANDARD,
    val taskId: String = "",
    val fileName: String = "",
    val totalFrames: Int = 0,
    val receivedCount: Int = 0,
    val missingFrames: List<Int> = emptyList(), // 1-based (仅标准模式使用)
    val isCompleted: Boolean = false,
    val isReceiving: Boolean = false,
    val elapsedSeconds: Long = 0,
    val assembledData: ByteArray? = null,
    val errorMessage: String? = null
) {
    val progress: Float
        get() = if (totalFrames > 0) receivedCount.toFloat() / totalFrames else 0f

    val progressPercent: Int
        get() = (progress * 100).toInt().coerceIn(0, 100)

    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (javaClass != other?.javaClass) return false
        other as TransferState
        return mode == other.mode &&
                taskId == other.taskId &&
                fileName == other.fileName &&
                totalFrames == other.totalFrames &&
                receivedCount == other.receivedCount &&
                missingFrames == other.missingFrames &&
                isCompleted == other.isCompleted &&
                isReceiving == other.isReceiving &&
                assembledData?.contentEquals(other.assembledData ?: ByteArray(0)) ?: (other.assembledData == null)
    }

    override fun hashCode(): Int {
        var result = mode.hashCode()
        result = 31 * result + taskId.hashCode()
        result = 31 * result + fileName.hashCode()
        result = 31 * result + totalFrames
        result = 31 * result + receivedCount
        result = 31 * result + missingFrames.hashCode()
        result = 31 * result + isCompleted.hashCode()
        result = 31 * result + isReceiving.hashCode()
        result = 31 * result + (assembledData?.contentHashCode() ?: 0)
        return result
    }
}

class TransferSessionManager {
    // 状态管理
    private var mode = TransferMode.STANDARD
    private var taskId: String = ""
    private var fileName: String = ""
    private var totalFrames: Int = 0
    private var startTime: Long = 0
    private var isCompleted: Boolean = false
    private var assembledData: ByteArray? = null

    // 标准模式存储
    private val standardChunks = mutableMapOf<Int, String>()

    // 喷泉码模式解码器
    private var fountainDecoder: FountainDecoder? = null

    @Synchronized
    fun onPacketScanned(packet: AirScanPacket): TransferState {
        // 同任务且已全部完成，直接幂等返回已完成态
        if (isCompleted && packet.taskId == this.taskId) {
            return getCurrentState()
        }

        when (packet) {
            is AirScanPacket.StandardFrame -> handleStandardFrame(packet)
            is AirScanPacket.FountainFrame -> handleFountainFrame(packet)
        }

        return getCurrentState()
    }

    private fun handleStandardFrame(frame: AirScanPacket.StandardFrame) {
        if (this.taskId != frame.taskId || this.mode != TransferMode.STANDARD) {
            resetForNewTask(TransferMode.STANDARD, frame.taskId, frame.fileName, frame.totalFrames)
        }

        if (!standardChunks.containsKey(frame.currentIndex)) {
            standardChunks[frame.currentIndex] = frame.payload
        }

        if (!isCompleted && standardChunks.size >= totalFrames && totalFrames > 0) {
            try {
                this.assembledData = ProtocolParser.assembleStandardFile(standardChunks, totalFrames)
                this.isCompleted = true
            } catch (e: Exception) {
                // error
            }
        }
    }

    private fun handleFountainFrame(frame: AirScanPacket.FountainFrame) {
        if (this.taskId != frame.taskId || this.mode != TransferMode.FOUNTAIN) {
            resetForNewTask(TransferMode.FOUNTAIN, frame.taskId, frame.fileName, frame.totalBlocks)
            fountainDecoder = FountainDecoder(
                taskId = frame.taskId,
                fileName = frame.fileName,
                totalBlocks = frame.totalBlocks,
                fileSize = frame.fileSize,
                chunkCapacity = frame.xorData.size
            )
        }

        val decoder = fountainDecoder ?: return
        if (!isCompleted) {
            decoder.processPacket(frame.seed, frame.xorData)
            if (decoder.isCompleted) {
                try {
                    this.assembledData = decoder.assemble()
                    this.isCompleted = true
                } catch (e: Exception) {
                    // error
                }
            }
        }
    }

    private fun resetForNewTask(newMode: TransferMode, newTid: String, newName: String, newTotal: Int) {
        this.mode = newMode
        this.taskId = newTid
        this.fileName = newName
        this.totalFrames = newTotal
        this.startTime = System.currentTimeMillis()
        this.isCompleted = false
        this.assembledData = null
        this.standardChunks.clear()
        this.fountainDecoder = null
    }

    @Synchronized
    fun reset(): TransferState {
        standardChunks.clear()
        fountainDecoder = null
        taskId = ""
        fileName = ""
        totalFrames = 0
        startTime = 0
        isCompleted = false
        assembledData = null
        mode = TransferMode.STANDARD
        return TransferState()
    }

    @Synchronized
    fun getAssembledData(): ByteArray? = assembledData

    @Synchronized
    private fun getCurrentState(errorMessage: String? = null): TransferState {
        val count = when (mode) {
            TransferMode.STANDARD -> standardChunks.size
            TransferMode.FOUNTAIN -> fountainDecoder?.solvedCount ?: 0
        }
        val missing = if (mode == TransferMode.STANDARD) {
            calculateMissingFrames(totalFrames, standardChunks.keys)
        } else {
            emptyList()
        }
        val elapsed = if (startTime > 0) (System.currentTimeMillis() - startTime) / 1000 else 0

        return TransferState(
            mode = mode,
            taskId = taskId,
            fileName = fileName,
            totalFrames = totalFrames,
            receivedCount = count,
            missingFrames = missing,
            isCompleted = isCompleted,
            isReceiving = !isCompleted && totalFrames > 0,
            elapsedSeconds = elapsed,
            assembledData = assembledData,
            errorMessage = errorMessage
        )
    }

    companion object {
        fun calculateMissingFrames(total: Int, receivedIndices: Set<Int>): List<Int> {
            if (total <= 0) return emptyList()
            val list = mutableListOf<Int>()
            for (i in 0 until total) {
                if (!receivedIndices.contains(i)) {
                    list.add(i + 1)
                }
            }
            return list
        }
    }
}
