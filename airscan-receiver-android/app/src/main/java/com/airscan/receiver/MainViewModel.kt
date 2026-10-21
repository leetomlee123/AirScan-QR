package com.airscan.receiver

import android.content.Context
import android.net.Uri
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.airscan.receiver.protocol.QrFrame
import com.airscan.receiver.protocol.TransferSessionManager
import com.airscan.receiver.protocol.TransferState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.io.OutputStream

class MainViewModel : ViewModel() {

    private val sessionManager = TransferSessionManager()
    private val _transferState = MutableStateFlow(TransferState())
    val transferState: StateFlow<TransferState> = _transferState.asStateFlow()

    fun onFrameScanned(frame: QrFrame) {
        val newState = sessionManager.onFrameScanned(frame)
        _transferState.value = newState
    }

    fun reset() {
        val newState = sessionManager.reset()
        _transferState.value = newState
    }

    fun saveFileToUri(context: Context, uri: Uri, onResult: (Boolean, String?) -> Unit) {
        val data = _transferState.value.assembledData
        if (data == null) {
            onResult(false, "没有可保存的文件数据")
            return
        }

        viewModelScope.launch {
            try {
                val outputStream: OutputStream? = context.contentResolver.openOutputStream(uri)
                if (outputStream == null) {
                    onResult(false, "无法打开输出流")
                    return@launch
                }
                outputStream.use { it.write(data) }
                onResult(true, null)
            } catch (e: Exception) {
                onResult(false, e.localizedMessage ?: "写入失败")
            }
        }
    }
}
