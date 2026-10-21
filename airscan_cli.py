#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
AirScan-QR Terminal CLI Sender
专为物理隔离机、无图形界面服务器、SSH 终端设计的纯命令行二维码广播工具。
零 GUI 依赖，基于 Unicode 半角块与 ANSI 颜色，可在任何终端中无闪烁高速播放。
"""

import os
import sys
import time
import base64
import random
import string
import argparse
import signal
import qrcode

# 终端推荐参数
DEFAULT_CHUNK_SIZE = 240   # 适配 80x24 标准终端屏幕高度
DEFAULT_FPS = 10           # 终端流式刷新帧率 (8-10 FPS)
DEFAULT_BORDER = 2         # 二维码静区留白 (2 格)

def generate_task_id(length=3):
    return ''.join(random.choices(string.ascii_uppercase + string.digits, k=length))

class AirScanCliSender:
    def __init__(self, file_path, chunk_size=DEFAULT_CHUNK_SIZE, fps=DEFAULT_FPS, border=DEFAULT_BORDER):
        if not os.path.isfile(file_path):
            raise FileNotFoundError(f"文件不存在: {file_path}")

        self.file_path = file_path
        self.file_name = os.path.basename(file_path)
        self.chunk_size = chunk_size
        self.fps = fps
        self.border = border
        self.task_id = generate_task_id()
        
        self.chunks = self._prepare_chunks()
        self.total_frames = len(self.chunks)
        self.running = True

        # 预先生成所有帧的 ANSI 字符，运行时 0 延迟循环推送
        self.frame_buffers = []

    def _prepare_chunks(self):
        with open(self.file_path, "rb") as f:
            raw_bytes = f.read()
        self.file_size = len(raw_bytes)
        b64_str = base64.b64encode(raw_bytes).decode("utf-8")
        return [b64_str[i:i + self.chunk_size] for i in range(0, len(b64_str), self.chunk_size)]

    def _render_qr_ansi(self, frame_idx):
        # 协议格式: TaskID|FileName|TotalFrames|CurrentIndex|DataPayload
        payload = f"{self.task_id}|{self.file_name}|{self.total_frames}|{frame_idx}|{self.chunks[frame_idx]}"
        
        qr = qrcode.QRCode(
            version=None,
            error_correction=qrcode.constants.ERROR_CORRECT_L,
            box_size=1,
            border=self.border
        )
        qr.add_data(payload)
        qr.make(fit=True)
        matrix = qr.get_matrix()

        rows = len(matrix)
        cols = len(matrix[0])
        lines = []

        # 使用 Unicode ▀ (U+2580) 半块字符，将两行像素压缩进一个终端字符行，保证 1:1 正方形比例
        for r in range(0, rows, 2):
            line = []
            for c in range(cols):
                top = matrix[r][c]
                bot = matrix[r + 1][c] if r + 1 < rows else False
                
                # top/bot: True 为黑色(二维码像素)，False 为白色(背景)
                if top and bot:
                    line.append("\033[30;40m▀")
                elif top and not bot:
                    line.append("\033[30;47m▀")
                elif not top and bot:
                    line.append("\033[37;40m▀")
                else:
                    line.append("\033[37;47m▀")
            line.append("\033[0m")
            lines.append("".join(line))

        return "\n".join(lines)

    def precompute(self):
        sys.stdout.write("正在预渲染二维码帧矩阵...\n")
        sys.stdout.flush()
        for idx in range(self.total_frames):
            ansi_qr = self._render_qr_ansi(idx)
            self.frame_buffers.append(ansi_qr)

    def start(self):
        self.precompute()

        # 隐藏光标，清屏
        sys.stdout.write("\033[?25l\033[2J")
        sys.stdout.flush()

        delay = 1.0 / self.fps
        frame_idx = 0

        try:
            while self.running:
                # \033[H 光标重置到左上角，实现 0 闪烁覆写
                header = (
                    f"\033[H\033[1;36m📡 AirScan-QR Terminal CLI Sender\033[0m\n"
                    f"文件: \033[1;32m{self.file_name}\033[0m ({self.file_size} B) | "
                    f"任务: \033[1;33m{self.task_id}\033[0m | "
                    f"帧率: \033[1;35m{self.fps} FPS\033[0m\n"
                    f"进度: 帧 \033[1;37m{frame_idx + 1}/{self.total_frames}\033[0m "
                    f"[{int((frame_idx + 1)/self.total_frames*100)}%]\n"
                )
                
                # 输出二维码
                qr_content = self.frame_buffers[frame_idx]
                footer = "\n\033[90m请用 Android 原生端对准终端扫码 | 按 Ctrl+C 退出\033[0m\n"

                sys.stdout.write(header + qr_content + footer)
                sys.stdout.flush()

                frame_idx = (frame_idx + 1) % self.total_frames
                time.sleep(delay)

        except KeyboardInterrupt:
            pass
        finally:
            # 恢复光标显示
            sys.stdout.write("\033[?25h\n\033[1;32m广播已停止。\033[0m\n")
            sys.stdout.flush()

def main():
    parser = argparse.ArgumentParser(
        description="AirScan-QR CLI - 纯终端动态二维码文件传输广播工具"
    )
    parser.add_argument("file", help="要广播传输的文件路径")
    parser.add_argument("-c", "--chunk-size", type=int, default=DEFAULT_CHUNK_SIZE,
                        help=f"单帧 Base64 切片大小 (默认: {DEFAULT_CHUNK_SIZE}，适配标准终端)")
    parser.add_argument("-f", "--fps", type=float, default=DEFAULT_FPS,
                        help=f"广播帧率 (默认: {DEFAULT_FPS} FPS)")
    parser.add_argument("-b", "--border", type=int, default=DEFAULT_BORDER,
                        help=f"静区边框宽度 (默认: {DEFAULT_BORDER})")

    args = parser.parse_args()

    sender = AirScanCliSender(
        file_path=args.file,
        chunk_size=args.chunk_size,
        fps=args.fps,
        border=args.border
    )
    sender.start()

if __name__ == "__main__":
    main()
