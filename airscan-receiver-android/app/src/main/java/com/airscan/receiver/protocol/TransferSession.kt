package com.airscan.receiver.protocol

data class TransferState(
    val taskId: String = "",
    val fileName: String = "",
    val totalFrames: Int = 0,
    val receivedCount: Int = 0,
    val missingFrames: List<Int> = emptyList(), // 1-based for UI display
    val isCompleted: Boolean = false,
    val isReceiving: Boolean = false,
    val elapsedSeconds: Long = 0,
    val assembledData: ByteArray? = null,
    val errorMessage: String? = null
) {
    val progress: Float
        get() = if (totalFrames > 0) receivedCount.toFloat() / totalFrames else 0f

    val progressPercent: Int
        get() = (progress * 100).toInt()

    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (javaClass != other?.javaClass) return false

        other as TransferState
        if (taskId != other.taskId) return false
        if (fileName != other.fileName) return false
        if (totalFrames != other.totalFrames) return false
        if (receivedCount != other.receivedCount) return false
        if (missingFrames != other.missingFrames) return false
        if (isCompleted != other.isCompleted) return false
        if (isReceiving != other.isReceiving) return false
        if (assembledData != null) {
            if (other.assembledData == null) return false
            if (!assembledData.contentEquals(other.assembledData)) return false
        } else if (other.assembledData != null) return false

        return true
    }

    override fun hashCode(): Int {
        var result = taskId.hashCode()
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
    private val chunks = mutableMapOf<Int, String>()
    private var taskId: String = ""
    private var fileName: String = ""
    private var totalFrames: Int = 0
    private var startTime: Long = 0
    private var isCompleted: Boolean = false
    private var assembledData: ByteArray? = null

    @Synchronized
    fun onFrameScanned(frame: QrFrame): TransferState {
        // 若当前任务已组包完成，且收到的是同任务帧，则直接返回已完成状态，严防 assembledData 被后续高频帧置空
        if (isCompleted && frame.taskId == this.taskId) {
            return getCurrentState()
        }

        // 识别到新 Task，重置状态
        if (frame.taskId != this.taskId) {
            this.taskId = frame.taskId
            this.fileName = frame.fileName
            this.totalFrames = frame.totalFrames
            this.chunks.clear()
            this.startTime = System.currentTimeMillis()
            this.isCompleted = false
            this.assembledData = null
        }

        // 存储新帧分片
        if (!chunks.containsKey(frame.currentIndex)) {
            chunks[frame.currentIndex] = frame.payload
        }

        // 检查是否集齐全部帧并组包
        var error: String? = null
        if (!isCompleted && chunks.size >= totalFrames && totalFrames > 0) {
            try {
                this.assembledData = ProtocolParser.assembleFile(chunks, totalFrames)
                this.isCompleted = true
            } catch (e: Exception) {
                error = "Assemble failed: ${e.message}"
            }
        }

        return getCurrentState(error)
    }

    @Synchronized
    fun reset(): TransferState {
        chunks.clear()
        taskId = ""
        fileName = ""
        totalFrames = 0
        startTime = 0
        isCompleted = false
        assembledData = null
        return TransferState()
    }

    @Synchronized
    fun getAssembledData(): ByteArray? = assembledData

    @Synchronized
    fun isFrameReceived(index: Int): Boolean = chunks.containsKey(index)

    @Synchronized
    private fun getCurrentState(errorMessage: String? = null): TransferState {
        val missing = calculateMissingFrames(totalFrames, chunks.keys)
        val elapsed = if (startTime > 0) (System.currentTimeMillis() - startTime) / 1000 else 0

        return TransferState(
            taskId = taskId,
            fileName = fileName,
            totalFrames = totalFrames,
            receivedCount = chunks.size,
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
                    list.add(i + 1) // 1-based display
                }
            }
            return list
        }
    }
}
