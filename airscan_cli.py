#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
AirScan-QR Terminal CLI Sender (v2.0)
专为物理隔离机、无图形界面服务器、SSH 终端设计的纯命令行二维码广播工具。
零 GUI 依赖，基于 Unicode 半角块与 ANSI 颜色，可在任何终端中无闪烁高速播放。

特性：
1. 默认自适应 Gzip 前置压缩（日志/文本切片数暴降 70%~90%）
2. 支持 -F / --frames 定向补帧与 -t / --task-id 复用
3. 支持 --no-gzip 原样未压缩传输
"""

import os
import sys
import time
import gzip
import base64
import random
import string
import shutil
import argparse
import qrcode

# 终端推荐参数
DEFAULT_CHUNK_SIZE = 240   # 适配 80x24 标准终端屏幕高度
DEFAULT_FPS = 10           # 终端流式刷新帧率 (8-10 FPS)
DEFAULT_BORDER = 2         # 二维码静区留白 (2 格)

ECC_MAP = {
    'L': qrcode.constants.ERROR_CORRECT_L,
    'M': qrcode.constants.ERROR_CORRECT_M,
    'Q': qrcode.constants.ERROR_CORRECT_Q,
    'H': qrcode.constants.ERROR_CORRECT_H,
}

def generate_task_id(length=3):
    return ''.join(random.choices(string.ascii_uppercase + string.digits, k=length))

def parse_frame_spec(spec_str, total_frames):
    """
    解析帧规格字符串，返回 0-based 的索引列表。
    输入为 1-based（与手机接收端 UI 上展示的缺失帧编号完全一致）。
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
                 border=DEFAULT_BORDER, task_id=None, frame_spec=None, use_gzip=True,
                 ecc='L', interval_ms=None):
        if not os.path.exists(file_path):
            raise FileNotFoundError(f"文件不存在: {file_path}")
        if os.path.isdir(file_path):
            raise IsADirectoryError(f"'{file_path}' 是一个目录，请指定文件或先压缩为单个归档文件")

        self.file_path = file_path
        self.file_name = os.path.basename(file_path)
        self.chunk_size = chunk_size
        
        # 兼容 -i (毫秒间隔) 与 -f (FPS)
        if interval_ms is not None and interval_ms > 0:
            self.fps = 1000.0 / interval_ms
            self.interval_sec = interval_ms / 1000.0
        else:
            self.fps = fps if fps > 0 else DEFAULT_FPS
            self.interval_sec = 1.0 / self.fps

        self.border = border
        self.use_gzip = use_gzip
        self.ecc_level = ECC_MAP.get(ecc.upper(), qrcode.constants.ERROR_CORRECT_L)
        self.ecc_name = ecc.upper()
        
        # 若指定 TaskID（如手机补帧场景），则复用，否则随机生成
        self.task_id = (task_id.strip().upper() if task_id else generate_task_id())
        
        self.chunks, self.is_compressed, self.orig_size, self.wire_size = self._prepare_chunks()
        self.total_frames = len(self.chunks)
        self.running = True

        # 解析指定要广播的帧（0-based 内部索引）
        self.active_frame_indices = parse_frame_spec(frame_spec, self.total_frames)
        self.is_subset = len(self.active_frame_indices) < self.total_frames

        # 帧缓冲区映射: frame_index -> ANSI QR 字符串 (按需即时生成并缓存)
        self.frame_buffers = {}

    def _prepare_chunks(self):
        with open(self.file_path, "rb") as f:
            raw_bytes = f.read()
        orig_size = len(raw_bytes)

        # 智能自适应压缩：若开启 gzip 且压缩后体积确实变小，则采用压缩流
        payload_bytes = raw_bytes
        is_compressed = False
        if self.use_gzip and orig_size > 64:
            gzipped = gzip.compress(raw_bytes, compresslevel=9)
            if len(gzipped) < orig_size:
                payload_bytes = gzipped
                is_compressed = True

        wire_size = len(payload_bytes)
        b64_str = base64.b64encode(payload_bytes).decode("utf-8")
        chunks = [b64_str[i:i + self.chunk_size] for i in range(0, len(b64_str), self.chunk_size)]
        if not chunks:
            # 空文件特殊处理
            chunks = [""]
        return chunks, is_compressed, orig_size, wire_size

    def _render_qr_ansi(self, frame_idx):
        # 协议格式: TaskID|FileName|TotalFrames|CurrentIndex|DataPayload
        payload = f"{self.task_id}|{self.file_name}|{self.total_frames}|{frame_idx}|{self.chunks[frame_idx]}"
        
        qr = qrcode.QRCode(
            version=None,
            error_correction=self.ecc_level,
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

        return "\n".join(lines), (rows + 1) // 2

    def get_frame(self, frame_idx):
        if frame_idx not in self.frame_buffers:
            content, line_count = self._render_qr_ansi(frame_idx)
            self.frame_buffers[frame_idx] = (content, line_count)
        return self.frame_buffers[frame_idx]

    def start(self):
        # 获取第一帧用于尺寸检测
        first_frame_idx = self.active_frame_indices[0]
        qr_content, qr_lines = self.get_frame(first_frame_idx)

        # 检查终端尺寸是否足够容纳
        term_cols, term_lines = shutil.get_terminal_size((80, 24))
        needed_lines = qr_lines + 6
        if term_lines < needed_lines:
            sys.stderr.write(
                f"\033[1;33m⚠️ 提示: 当前终端高度为 {term_lines} 行，而二维码展示需要约 {needed_lines} 行。\033[0m\n"
                f"\033[90m若二维码发生换行滚动导致扫码困难，建议全屏终端或使用推荐切片大小: -s 240\033[0m\n"
                f"3秒后自动开始播放...\n"
            )
            time.sleep(2)

        # 隐藏光标，清屏
        sys.stdout.write("\033[?25l\033[2J")
        sys.stdout.flush()

        seq_idx = 0
        total_active = len(self.active_frame_indices)

        ratio_info = ""
        if self.is_compressed:
            saved_pct = (1.0 - (self.wire_size / self.orig_size)) * 100
            ratio_info = f"\033[1;32m[Gzip 压缩 -{saved_pct:.1f}%: {self.orig_size}B → {self.wire_size}B]\033[0m"

        try:
            while self.running:
                real_frame_idx = self.active_frame_indices[seq_idx]
                qr_content, _ = self.get_frame(real_frame_idx)
                
                # 构建顶部状态提示
                mode_badge = (
                    f"\033[1;33m[定向补帧: 轮播 {total_active}/{self.total_frames} 帧]\033[0m"
                    if self.is_subset else
                    f"\033[1;32m[全量广播模式]\033[0m"
                )

                header = (
                    f"\033[H\033[1;36m📡 AirScan-QR CLI\033[0m {mode_badge} {ratio_info}\n"
                    f"文件: \033[1;32m{self.file_name}\033[0m | "
                    f"任务ID: \033[1;33m{self.task_id}\033[0m | "
                    f"切片: \033[1;34m{self.chunk_size}B\033[0m | "
                    f"纠错: \033[1;34m{self.ecc_name}\033[0m | "
                    f"帧率: \033[1;35m{self.fps:.1f} FPS ({int(self.interval_sec*1000)}ms)\033[0m\n"
                    f"当前广播: 帧 \033[1;37m{real_frame_idx + 1}\033[0m / {self.total_frames} "
                    f"(轮播序号: {seq_idx + 1}/{total_active})\n"
                )
                
                footer = "\n\033[90m请用 Android 原生端对准终端扫码 | 按 Ctrl+C 退出\033[0m\n"

                sys.stdout.write(header + qr_content + footer)
                sys.stdout.flush()

                seq_idx = (seq_idx + 1) % total_active
                time.sleep(self.interval_sec)

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
    parser.add_argument("-s", "-c", "--chunk-size", "--size", dest="chunk_size", type=int, default=DEFAULT_CHUNK_SIZE,
                        help=f"单帧切片大小 (默认: {DEFAULT_CHUNK_SIZE}，适配标准 80x24 终端)")
    parser.add_argument("-f", "--fps", type=float, default=DEFAULT_FPS,
                        help=f"广播帧率 FPS (默认: {DEFAULT_FPS})")
    parser.add_argument("-i", "--interval", type=float, default=None,
                        help="每帧间隔毫秒数 ms (例如: -i 50 即 50ms 一帧，相当于 20 FPS)")
    parser.add_argument("-e", "--ecc", "--error-correction", dest="ecc", type=str, default="L",
                        choices=["L", "M", "Q", "H", "l", "m", "q", "h"],
                        help="纠错等级: L(7%), M(15%), Q(25%), H(30%) (默认: L，切片数最少)")
    parser.add_argument("-b", "--border", type=int, default=DEFAULT_BORDER,
                        help=f"静区边框宽度 (默认: {DEFAULT_BORDER})")
    parser.add_argument("-F", "--frames", type=str, default=None,
                        help="仅显示/广播指定帧号（1-based，与手机端缺失帧编号完全一致，支持 '7,12,25' 或 '5-10' 等）")
    parser.add_argument("-t", "--task-id", type=str, default=None,
                        help="指定 Task ID（若给手机补帧，必须指定为手机上显示的相同 Task ID，如 -t ABC）")
    parser.add_argument("--no-gzip", action="store_true",
                        help="禁用 Gzip 自动前置压缩（默认自动启用 Gzip）")

    args = parser.parse_args()

    if not os.path.exists(args.file):
        sys.stderr.write(f"\033[1;31m❌ 错误: 找不到文件 '{args.file}'\033[0m\n")
        sys.stderr.write(f"\033[1;33m💡 提示: 请将 '{args.file}' 替换为您电脑中实际存在的文件路径，例如:\033[0m\n")
        sys.stderr.write(f"   python airscan_cli.py your_file.txt -s 800 -i 50 -e L\n")
        sys.exit(1)

    try:
        sender = AirScanCliSender(
            file_path=args.file,
            chunk_size=args.chunk_size,
            fps=args.fps,
            interval_ms=args.interval,
            ecc=args.ecc,
            border=args.border,
            task_id=args.task_id,
            frame_spec=args.frames,
            use_gzip=not args.no_gzip
        )
        sender.start()
    except Exception as e:
        sys.stderr.write(f"\033[1;31m❌ 启动失败: {e}\033[0m\n")
        sys.exit(1)

if __name__ == "__main__":
    main()
