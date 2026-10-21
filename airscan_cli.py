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
import qrcode

# 终端推荐参数
DEFAULT_CHUNK_SIZE = 240   # 适配 80x24 标准终端屏幕高度
DEFAULT_FPS = 10           # 终端流式刷新帧率 (8-10 FPS)
DEFAULT_BORDER = 2         # 二维码静区留白 (2 格)

def generate_task_id(length=3):
    return ''.join(random.choices(string.ascii_uppercase + string.digits, k=length))

def parse_frame_spec(spec_str, total_frames):
    """
    解析帧规格字符串，返回 0-based 的索引列表。
    输入为 1-based（与手机接收端 UI 上展示的缺失帧编号完全一致）。
    支持格式:
      '7'            -> 帧 7
      '7,12,25'      -> 帧 7, 12, 25
      '5-10'         -> 帧 5 到 10
      '1,3,5-8,12'   -> 混合格式
      '7 12 25'      -> 空格分隔
    """
    if not spec_str:
        return list(range(total_frames))

    selected = set()
    parts = spec_str.replace(' ', ',').split(',')
    ignored = []

    for part in parts:
        part = part.strip()
        if not part:
            continue
        try:
            if '-' in part:
                start_str, end_str = part.split('-', 1)
                start = int(start_str.strip())
                end = int(end_str.strip())
                for n in range(min(start, end), max(start, end) + 1):
                    if 1 <= n <= total_frames:
                        selected.add(n - 1)
                    else:
                        ignored.append(n)
            else:
                n = int(part)
                if 1 <= n <= total_frames:
                    selected.add(n - 1)
                else:
                    ignored.append(n)
        except ValueError:
            sys.stderr.write(f"\033[33m警告: 无法解析的帧参数 '{part}'，已忽略\033[0m\n")

    if ignored:
        sys.stderr.write(f"\033[33m提示: 帧 {ignored} 超出总帧数范围 (1~{total_frames})，已自动忽略\033[0m\n")

    res = sorted(list(selected))
    if not res:
        raise ValueError(f"没有找到有效的帧编号！(有效范围: 1 ~ {total_frames})")
    return res

class AirScanCliSender:
    def __init__(self, file_path, chunk_size=DEFAULT_CHUNK_SIZE, fps=DEFAULT_FPS,
                 border=DEFAULT_BORDER, task_id=None, frame_spec=None):
        if not os.path.isfile(file_path):
            raise FileNotFoundError(f"文件不存在: {file_path}")

        self.file_path = file_path
        self.file_name = os.path.basename(file_path)
        self.chunk_size = chunk_size
        self.fps = fps
        self.border = border
        
        # 若指定 TaskID（如手机补帧场景），则复用，否则随机生成
        self.task_id = (task_id.strip().upper() if task_id else generate_task_id())
        
        self.chunks = self._prepare_chunks()
        self.total_frames = len(self.chunks)
        self.running = True

        # 解析指定要广播的帧（0-based 内部索引）
        self.active_frame_indices = parse_frame_spec(frame_spec, self.total_frames)
        self.is_subset = len(self.active_frame_indices) < self.total_frames

        # 预渲染映射表: frame_index -> ANSI QR 字符串
        self.frame_buffers = {}

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
        count = len(self.active_frame_indices)
        sys.stdout.write(f"正在预渲染 {count} 帧二维码矩阵...\n")
        sys.stdout.flush()
        for idx in self.active_frame_indices:
            self.frame_buffers[idx] = self._render_qr_ansi(idx)

    def start(self):
        self.precompute()

        # 隐藏光标，清屏
        sys.stdout.write("\033[?25l\033[2J")
        sys.stdout.flush()

        delay = 1.0 / self.fps
        seq_idx = 0
        total_active = len(self.active_frame_indices)

        try:
            while self.running:
                real_frame_idx = self.active_frame_indices[seq_idx]
                
                # 构建顶部状态提示
                mode_badge = (
                    f"\033[1;33m[定向补帧模式: 广播 {total_active}/{self.total_frames} 帧]\033[0m"
                    if self.is_subset else
                    f"\033[1;32m[全量广播模式]\033[0m"
                )

                header = (
                    f"\033[H\033[1;36m📡 AirScan-QR Terminal CLI Sender\033[0m {mode_badge}\n"
                    f"文件: \033[1;32m{self.file_name}\033[0m ({self.file_size} B) | "
                    f"任务ID: \033[1;33m{self.task_id}\033[0m | "
                    f"帧率: \033[1;35m{self.fps} FPS\033[0m\n"
                    f"当前广播: 帧 \033[1;37m{real_frame_idx + 1}\033[0m / {self.total_frames} "
                    f"(轮播进度: {seq_idx + 1}/{total_active})\n"
                )
                
                # 输出二维码
                qr_content = self.frame_buffers[real_frame_idx]
                footer = "\n\033[90m请用 Android 原生端对准终端扫码 | 按 Ctrl+C 退出\033[0m\n"

                sys.stdout.write(header + qr_content + footer)
                sys.stdout.flush()

                seq_idx = (seq_idx + 1) % total_active
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
    parser.add_argument("-F", "--frames", type=str, default=None,
                        help="仅显示/广播指定帧号（1-based，与手机端缺失帧编号完全一致，支持 '7,12,25' 或 '5-10' 等）")
    parser.add_argument("-t", "--task-id", type=str, default=None,
                        help="指定 Task ID（若给手机补帧，必须指定为手机上显示的相同 Task ID，如 -t ABC）")

    args = parser.parse_args()

    sender = AirScanCliSender(
        file_path=args.file,
        chunk_size=args.chunk_size,
        fps=args.fps,
        border=args.border,
        task_id=args.task_id,
        frame_spec=args.frames
    )
    sender.start()

if __name__ == "__main__":
    main()
