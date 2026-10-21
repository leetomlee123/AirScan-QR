package com.airscan.receiver

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Bundle
import android.view.WindowManager
import android.webkit.MimeTypeMap
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.ContextCompat
import com.airscan.receiver.ui.AirScanTheme
import com.airscan.receiver.ui.TransferScreen
import java.io.File

class MainActivity : ComponentActivity() {

    private val viewModel: MainViewModel by viewModels()
    private var hasCameraPermission by mutableStateOf(false)

    // Permission launcher
    private val requestPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { isGranted ->
        hasCameraPermission = isGranted
        if (!isGranted) {
            Toast.makeText(this, "需要相机权限以进行扫码识别", Toast.LENGTH_SHORT).show()
        }
    }

    // Storage SAF launcher
    private val createDocumentLauncher = registerForActivityResult(
        ActivityResultContracts.CreateDocument("*/*")
    ) { uri: Uri? ->
        if (uri != null) {
            viewModel.saveFileToUri(this, uri) { success, errorMsg ->
                if (success) {
                    Toast.makeText(this, "文件已成功保存！", Toast.LENGTH_LONG).show()
                } else {
                    Toast.makeText(this, "保存失败: $errorMsg", Toast.LENGTH_LONG).show()
                }
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // 保持屏幕常亮，防止扫码过程中灭屏休眠
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        checkCameraPermission()

        setContent {
            AirScanTheme {
                val state by viewModel.transferState.collectAsState()

                TransferScreen(
                    state = state,
                    hasCameraPermission = hasCameraPermission,
                    onRequestCameraPermission = {
                        requestPermissionLauncher.launch(Manifest.permission.CAMERA)
                    },
                    onPacketScanned = { packet ->
                        viewModel.onPacketScanned(packet)
                    },
                    onSaveFileClicked = {
                        val fileName = state.fileName.ifEmpty { "received_file" }
                        val mimeType = getMimeType(fileName)
                        createDocumentLauncher.launch(fileName)
                    },
                    onResetClicked = {
                        viewModel.reset()
                    }
                )
            }
        }
    }

    private fun checkCameraPermission() {
        hasCameraPermission = ContextCompat.checkSelfPermission(
            this,
            Manifest.permission.CAMERA
        ) == PackageManager.PERMISSION_GRANTED

        if (!hasCameraPermission) {
            requestPermissionLauncher.launch(Manifest.permission.CAMERA)
        }
    }

    private fun getMimeType(fileName: String): String {
        val extension = File(fileName).extension
        return if (extension.isNotEmpty()) {
            MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension.lowercase()) ?: "*/*"
        } else {
            "*/*"
        }
    }
}
