package com.airscan.receiver.protocol

/**
 * 喷泉码（Luby Transform Codes）解码核心算法实现。
 * 与 airscan-fountain.html 及 Go 接收端保持 100% 互操作性。
 */
class FountainDecoder(
    val taskId: String,
    val fileName: String,
    val totalBlocks: Int,
    val fileSize: Int,
    val chunkCapacity: Int
) {
    // 线性同余随机数发生器 (确保与发送端完全对齐)
    class PRNG(private var seed: Long) {
        fun next(): Double {
            seed = (seed * 16807L) % 2147483647L
            return (seed - 1.0) / 2147483646.0
        }
    }

    data class QueueItem(
        var idxs: List<Int>,
        val data: ByteArray
    ) {
        override fun equals(other: Any?): Boolean {
            if (this === other) return true
            if (javaClass != other?.javaClass) return false
            other as QueueItem
            return idxs == other.idxs && data.contentEquals(other.data)
        }

        override fun hashCode(): Int {
            var result = idxs.hashCode()
            result = 31 * result + data.contentHashCode()
            return result
        }
    }

    private val solved = mutableMapOf<Int, ByteArray>()
    private val queue = mutableListOf<QueueItem>()

    val solvedCount: Int
        @Synchronized get() = solved.size

    val isCompleted: Boolean
        @Synchronized get() = solved.size >= totalBlocks

    /**
     * 根据种子计算该水滴包覆盖的原始块索引列表
     */
    fun getDegrees(seed: Long, total: Int): List<Int> {
        val rng = PRNG(seed)
        val p = rng.next()
        val d = when {
            p < 0.4 -> 1
            p < 0.7 -> 2
            p < 0.9 -> (rng.next() * 10).toInt() + 3
            else -> (rng.next() * total * 0.5).toInt() + 2
        }.coerceAtMost(total).coerceAtLeast(1)

        val idxSet = mutableSetOf<Int>()
        while (idxSet.size < d) {
            val x = (rng.next() * total).toInt().coerceIn(0, total - 1)
            idxSet.add(x)
        }
        return idxSet.sorted()
    }

    /**
     * 摄入一个喷泉码水滴包，执行消元与高斯回代求解
     */
    @Synchronized
    fun processPacket(seed: Long, data: ByteArray): Boolean {
        if (isCompleted) return false

        val idxs = getDegrees(seed, totalBlocks)
        if (totalBlocks == 1) {
            if (!solved.containsKey(0)) {
                solved[0] = data
                return true
            }
            return false
        }

        val curData = data.copyOf()
        var remainingIdxs = mutableListOf<Int>()

        // 1. 消元已有块
        for (idx in idxs) {
            val solvedChunk = solved[idx]
            if (solvedChunk != null) {
                xorBytes(curData, solvedChunk)
            } else {
                remainingIdxs.add(idx)
            }
        }

        if (remainingIdxs.isEmpty()) return false

        // 2. 利用队列中的方程消元
        val queueIterator = queue.iterator()
        while (queueIterator.hasNext() && remainingIdxs.isNotEmpty()) {
            val item = queueIterator.next()
            if (item.idxs.isNotEmpty() && item.idxs[0] == remainingIdxs[0]) {
                xorBytes(curData, item.data)
                remainingIdxs = xorIdxLists(remainingIdxs, item.idxs)
            }
        }

        if (remainingIdxs.isNotEmpty()) {
            queue.add(QueueItem(remainingIdxs, curData))
            queue.sortBy { it.idxs[0] }
        }

        // 3. 回代求解度数为 1 的方程
        var foundNew = true
        var newlySolved = false
        while (foundNew) {
            foundNew = false
            for (i in queue.indices) {
                val item = queue[i]
                if (item.idxs.size == 1) {
                    val sIdx = item.idxs[0]
                    val sData = item.data
                    solved[sIdx] = sData
                    queue.removeAt(i)
                    foundNew = true
                    newlySolved = true

                    // 将新解出的块代入队列其余方程
                    for (qItem in queue) {
                        if (qItem.idxs.contains(sIdx)) {
                            xorBytes(qItem.data, sData)
                            qItem.idxs = qItem.idxs.filter { it != sIdx }
                        }
                    }
                    break
                }
            }
        }

        return newlySolved
    }

    /**
     * 重组出完整原始二进制文件
     */
    @Synchronized
    fun assemble(): ByteArray {
        val finalBuf = ByteArray(fileSize)
        val l = chunkCapacity
        for (i in 0 until totalBlocks) {
            val start = i * l
            if (start < fileSize) {
                val len = (fileSize - start).coerceAtMost(l)
                val chunk = solved[i] ?: throw IllegalStateException("Block $i not yet decoded")
                System.arraycopy(chunk, 0, finalBuf, start, len)
            }
        }
        return ProtocolParser.decompressIfGzip(finalBuf)
    }

    companion object {
        fun xorBytes(dst: ByteArray, src: ByteArray) {
            val n = dst.size.coerceAtMost(src.size)
            for (i in 0 until n) {
                dst[i] = (dst[i].toInt() xor src[i].toInt()).toByte()
            }
        }

        fun xorIdxLists(a: List<Int>, b: List<Int>): MutableList<Int> {
            val set = a.toMutableSet()
            for (x in b) {
                if (set.contains(x)) set.remove(x) else set.add(x)
            }
            return set.sorted().toMutableList()
        }
    }
}
