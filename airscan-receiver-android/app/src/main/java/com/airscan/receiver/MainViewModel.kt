package com.airscan.receiver

import android.content.Context
import android.net.Uri
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.airscan.receiver.protocol.QrFrame
import com.airscan.receiver.protocol.TransferSessionManager
import com.airscan.receiver.protocol.TransferState
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
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
        val data = _transferState.value.assembledData ?: sessionManager.getAssembledData()
        if (data == null || data.isEmpty()) {
            onResult(false, "没有可保存的文件数据 (数据为空)")
            return
        }

        viewModelScope.launch(Dispatchers.IO) {
            try {
                val outputStream: OutputStream? = context.contentResolver.openOutputStream(uri)
                if (outputStream == null) {
                    withContext(Dispatchers.Main) {
                        onResult(false, "无法打开系统输出流")
                    }
                    return@launch
                }
                outputStream.use { os ->
                    os.write(data)
                    os.flush()
                }
                withContext(Dispatchers.Main) {
                    onResult(true, null)
                }
            } catch (e: Exception) {
                withContext(Dispatchers.Main) {
                    onResult(false, e.localizedMessage ?: "写入失败")
                }
            }
        }
    }
}
