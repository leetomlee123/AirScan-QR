#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
AirScan-QR Terminal CLI Sender (v3.0)
专为物理隔离机、无图形界面服务器、SSH 终端设计的纯命令行二维码广播工具。
零 GUI 依赖，基于 Unicode 半角块与 ANSI 颜色，可在任何终端中无闪烁高速播放。

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  快速使用命令
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  【模式一：标准单码模式（默认）】
    适合：80×24 标准小终端、SSH 连接、兼容性优先
    python airscan_cli.py file.txt
    python airscan_cli.py file.txt -s 400 -f 10        # 指定切片大小和帧率
    python airscan_cli.py file.txt --auto               # 按终端尺寸自动最大化切片
    python airscan_cli.py file.txt -e M                 # 提高纠错等级（抗模糊）

  【模式二：喷泉码四象限模式（推荐）】
    适合：全屏终端、笔记本屏幕、追求最高吞吐量
    python airscan_cli.py file.txt --fountain           # 同屏 4 个小码，无需补帧
    python airscan_cli.py file.txt --fountain --auto    # 自适应终端尺寸
    python airscan_cli.py file.txt --fountain -f 15     # 15 FPS 高速广播
    python airscan_cli.py file.txt --fountain -e M      # 提高纠错抵抗 RDP 压缩失真

  【模式三：定向补帧（标准模式专用）】
    适合：手机端显示有缺失帧，按帧号精准补发
    python airscan_cli.py file.txt -t ABC -F 5,12,20    # 补发指定帧（逗号分隔）
    python airscan_cli.py file.txt -t ABC -F 5-10       # 补发连续帧范围

  【通用选项】
    -s N        切片大小（标准模式字节数，喷泉模式原始字节数，默认 240）
    -f N        帧率 FPS（默认 10）
    -i N        帧间隔毫秒（-i 50 = 20FPS，与 -f 互斥，优先生效）
    -e L/M/Q/H  纠错等级（L=7%% M=15%% Q=25%% H=30%%，默认 L）
    -b N        静区宽度（默认 1，越小码越紧凑）
    --no-gzip   禁用 Gzip 压缩（默认自动启用）
    --auto      按当前终端实际尺寸自动计算最大切片
    --fountain  启用喷泉码四象限模式

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

特性：
1. 默认自适应 Gzip 前置压缩（日志/文本切片数暴降 70%~90%）
2. 支持 -F / --frames 定向补帧与 -t / --task-id 复用
3. 支持 --no-gzip 原样未压缩传输
4. 支持 --auto 屏幕自适应切片：按笔记本/全屏终端实测尺寸自动计算最大 chunk_size
5. 支持 --fountain 喷泉码四象限模式：同屏显示 4 个小码，吞吐量最高 4×
"""

import os
import sys
import time
import gzip
import base64
import math
import random
import string
import shutil
import argparse
import urllib.parse
import qrcode

# 终端推荐参数
DEFAULT_CHUNK_SIZE = 240   # 适配 80x24 标准终端屏幕高度
DEFAULT_FPS = 10           # 终端流式刷新帧率 (8-10 FPS)
DEFAULT_BORDER = 1         # 二维码静区留白 (喷泉码模式下缩小为 1)

ECC_MAP = {
    'L': qrcode.constants.ERROR_CORRECT_L,
    'M': qrcode.constants.ERROR_CORRECT_M,
    'Q': qrcode.constants.ERROR_CORRECT_Q,
    'H': qrcode.constants.ERROR_CORRECT_H,
}

# ───────── 喷泉码（LT Codes）PRNG & 度分布 ─────────

def _prng_next(seed):
    """线性同余发生器，与 JS/Go/Android 实现完全对齐。"""
    seed = (seed * 16807) % 2147483647
    return seed, (seed - 1) / 2147483646.0

def _get_degree(L, seed):
    """根据种子计算水滴度数（覆盖的原始块数量），与前端/Go 端对齐。"""
    seed, p = _prng_next(seed)
    if p < 0.4:
        d = 1
    elif p < 0.7:
        d = 2
    elif p < 0.9:
        seed, v = _prng_next(seed)
        d = int(v * 10) + 3
    else:
        seed, v = _prng_next(seed)
        d = int(v * L * 0.5) + 2
    return seed, min(d, L)

def _get_idxs(seed, L):
    """给定种子，返回该水滴包覆盖的原始块索引列表（有序，无重复）。"""
    seed, d = _get_degree(L, seed)
    idxs = []
    seen = set()
    while len(idxs) < d:
        seed, v = _prng_next(seed)
        x = int(v * L) % L
        if x not in seen:
            seen.add(x)
            idxs.append(x)
    return sorted(idxs)

def _xor_bytes(a, b):
    return bytes(x ^ y for x, y in zip(a, b))

def make_fountain_packet(chunks, seed, task_id, file_name, file_size):
    """
    生成一个喷泉码水滴包，返回 QR 内容字符串。
    协议格式：TaskID|Seed|TotalBlocks|Base64(XorData)|UrlFileName:FileSize
    """
    L = len(chunks)
    idxs = _get_idxs(seed, L)
    chunk_size = len(chunks[0])
    xor_data = bytearray(chunk_size)
    for i in idxs:
        xor_data = bytearray(_xor_bytes(xor_data, chunks[i]))
    b64 = base64.b64encode(bytes(xor_data)).decode('ascii')
    meta = f"{urllib.parse.quote(file_name, safe='')}:{file_size}"
    return f"{task_id}|{seed}|{L}|{b64}|{meta}"

# ───────── 工具函数 ─────────

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

# ───────── 二维码渲染 ─────────

def render_qr_ansi(payload, ecc_level, border):
    """将字符串 payload 渲染为 ANSI 半块字符二维码，返回 (字符串, 行数, 列数)。"""
    qr = qrcode.QRCode(
        version=None,
        error_correction=ecc_level,
        box_size=1,
        border=border,
    )
    qr.add_data(payload)
    qr.make(fit=True)
    matrix = qr.get_matrix()

    rows = len(matrix)
    cols = len(matrix[0])
    lines = []

    for r in range(0, rows, 2):
        line = []
        for c in range(cols):
            top = matrix[r][c]
            bot = matrix[r + 1][c] if r + 1 < rows else False
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

    return "\n".join(lines), (rows + 1) // 2, cols

def compose_2x2_frame(qr_strings_and_sizes):
    """
    将 4 个 QR ANSI 字符串排列为 2×2 的四象限布局。
    qr_strings_and_sizes: [(ansi_str, line_count, col_count), ...]
    返回 (完整 ANSI 字符串, 总行数)
    """
    if len(qr_strings_and_sizes) != 4:
        raise ValueError("需要恰好 4 个二维码")

    # 拆分每个 QR 为行数组
    qr_lines = [s.split("\n") for s, _, _ in qr_strings_and_sizes]
    max_h = max(len(lines) for lines in qr_lines)

    # 补齐短码到相同行数
    for lines in qr_lines:
        col_width = len(lines[0]) if lines else 0
        while len(lines) < max_h:
            lines.append(" " * col_width)

    composed = []
    for row in range(max_h):
        # 左上 | 右上
        top_row = qr_lines[0][row] + "  " + qr_lines[1][row]
        composed.append(top_row)
    composed.append("")  # 两行之间空一行
    for row in range(max_h):
        # 左下 | 右下
        bot_row = qr_lines[2][row] + "  " + qr_lines[3][row]
        composed.append(bot_row)

    return "\n".join(composed), max_h * 2 + 1

# ───────── 主发送器类 ─────────

class AirScanCliSender:
    def __init__(self, file_path, chunk_size=DEFAULT_CHUNK_SIZE, fps=DEFAULT_FPS,
                 border=DEFAULT_BORDER, task_id=None, frame_spec=None, use_gzip=True,
                 ecc='L', interval_ms=None, auto_size=False, fountain=False):
        self.auto_size = auto_size
        self.fountain = fountain

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

        # 笔记本/全屏终端屏幕自适应
        if auto_size and not fountain:
            self.chunk_size = self._fit_chunk_size()

        self.chunks, self.is_compressed, self.orig_size, self.wire_size, self.raw_payload_bytes = \
            self._prepare_chunks()
        self.total_frames = len(self.chunks)
        self.running = True

        # 帧缓冲（标准模式）
        self.frame_buffers = {}

        # 喷泉码模式不使用 parse_frame_spec，种子是无限流
        if not fountain:
            self.active_frame_indices = parse_frame_spec(frame_spec, self.total_frames)
            self.is_subset = len(self.active_frame_indices) < self.total_frames
        else:
            self.active_frame_indices = []
            self.is_subset = False

    def _fit_chunk_size(self, reserve_lines=6):
        """
        屏幕自适应：按当前终端尺寸用二分法探测能容纳的最大 chunk_size。
        """
        term_cols, term_lines = shutil.get_terminal_size((80, 24))
        avail_cols = max(term_cols - 1, 20)
        avail_lines = max(term_lines - reserve_lines, 4)

        def fits(size):
            probe = f"{self.task_id}|{self.file_name}|9999|9999|" + "A" * size
            qr = qrcode.QRCode(
                version=None,
                error_correction=self.ecc_level,
                box_size=1,
                border=self.border
            )
            qr.add_data(probe)
            qr.make(fit=True)
            matrix = qr.get_matrix()
            rows, cols = len(matrix), len(matrix[0])
            return cols <= avail_cols and (rows + 1) // 2 <= avail_lines

        lo, hi = 8, 2800
        if not fits(lo):
            return lo

        best = lo
        while lo <= hi:
            mid = (lo + hi) // 2
            if fits(mid):
                best = mid
                lo = mid + 1
            else:
                hi = mid - 1
        return best

    def _fit_fountain_chunk_size(self):
        """
        喷泉码四象限模式：按终端尺寸自适应，目标是每个小码宽度 ≤ (终端宽-2)/2。
        二分探测单个小码能放下的最大 chunk_size。
        """
        term_cols, term_lines = shutil.get_terminal_size((80, 24))
        # 每个小码列数 <= (总列数 - 2间距) / 2，行数 <= (总行数 - header/footer - 1间距) / 2
        avail_cols = max((term_cols - 2) // 2 - 1, 10)
        avail_lines = max((term_lines - 6 - 1) // 2, 4)

        def fits(size):
            # 用最坏情况的喷泉包 payload 长度来探测
            dummy_b64 = "A" * int(size * 4 / 3 + 4)
            probe = f"{self.task_id}|999999|99|{dummy_b64}|test%20file:99999"
            qr = qrcode.QRCode(
                version=None,
                error_correction=self.ecc_level,
                box_size=1,
                border=self.border,
            )
            qr.add_data(probe)
            qr.make(fit=True)
            matrix = qr.get_matrix()
            rows, cols = len(matrix), len(matrix[0])
            return cols <= avail_cols and (rows + 1) // 2 <= avail_lines

        lo, hi = 8, 1200
        if not fits(lo):
            return lo
        best = lo
        while lo <= hi:
            mid = (lo + hi) // 2
            if fits(mid):
                best = mid
                lo = mid + 1
            else:
                hi = mid - 1
        return best

    def _prepare_chunks(self):
        with open(self.file_path, "rb") as f:
            raw_bytes = f.read()
        orig_size = len(raw_bytes)

        payload_bytes = raw_bytes
        is_compressed = False
        if self.use_gzip and orig_size > 64:
            gzipped = gzip.compress(raw_bytes, compresslevel=9)
            if len(gzipped) < orig_size:
                payload_bytes = gzipped
                is_compressed = True

        wire_size = len(payload_bytes)

        if self.fountain:
            # 喷泉码模式：直接按 chunk_size 分割原始字节，不走 Base64
            cs = self.chunk_size
            chunks = []
            for i in range(0, wire_size, cs):
                block = payload_bytes[i:i + cs]
                # 末尾块补齐到 chunk_size（XOR 运算需要等长块）
                if len(block) < cs:
                    block = block + bytes(cs - len(block))
                chunks.append(bytes(block))
            if not chunks:
                chunks = [bytes(cs)]
            return chunks, is_compressed, orig_size, wire_size, payload_bytes
        else:
            b64_str = base64.b64encode(payload_bytes).decode("utf-8")
            chunks = [b64_str[i:i + self.chunk_size] for i in range(0, len(b64_str), self.chunk_size)]
            if not chunks:
                chunks = [""]
            return chunks, is_compressed, orig_size, wire_size, payload_bytes

    def _render_qr_ansi(self, frame_idx):
        """标准模式：生成单帧 ANSI QR 字符串。"""
        payload = f"{self.task_id}|{self.file_name}|{self.total_frames}|{frame_idx}|{self.chunks[frame_idx]}"
        qr_str, line_count, col_count = render_qr_ansi(payload, self.ecc_level, self.border)
        return qr_str, line_count

    def get_frame(self, frame_idx):
        if frame_idx not in self.frame_buffers:
            content, line_count = self._render_qr_ansi(frame_idx)
            self.frame_buffers[frame_idx] = (content, line_count)
        return self.frame_buffers[frame_idx]

    # ── 喷泉码模式主循环 ──

    def _start_fountain(self):
        """喷泉码四象限广播主循环。"""
        # 自适应小码尺寸
        if self.auto_size:
            self.chunk_size = self._fit_fountain_chunk_size()
            # 重新切块
            self.chunks, self.is_compressed, self.orig_size, self.wire_size, self.raw_payload_bytes = \
                self._prepare_chunks()
            self.total_frames = len(self.chunks)

        term_cols, term_lines = shutil.get_terminal_size((80, 24))

        ratio_info = ""
        if self.is_compressed:
            saved_pct = (1.0 - (self.wire_size / self.orig_size)) * 100
            ratio_info = f"\033[1;32m[Gzip -{saved_pct:.1f}%: {self.orig_size}B→{self.wire_size}B]\033[0m"

        sys.stdout.write("\033[?25l\033[2J")
        sys.stdout.flush()

        seed_counter = random.randint(1, 2147483000)

        try:
            frame_count = 0
            while self.running:
                # 每帧生成 4 个不同种子的水滴包
                seeds = [seed_counter + i for i in range(4)]
                seed_counter += 4

                qr_data = []
                for seed in seeds:
                    payload = make_fountain_packet(
                        self.chunks, seed % 2147483646 + 1,
                        self.task_id, self.file_name, self.wire_size
                    )
                    qr_str, line_count, col_count = render_qr_ansi(payload, self.ecc_level, self.border)
                    qr_data.append((qr_str, line_count, col_count))

                composed, total_lines = compose_2x2_frame(qr_data)

                header = (
                    f"\033[H\033[1;35m🌊 AirScan-QR CLI [喷泉码四象限]\033[0m {ratio_info}\n"
                    f"文件: \033[1;32m{self.file_name}\033[0m | "
                    f"任务: \033[1;33m{self.task_id}\033[0m | "
                    f"块大小: \033[1;34m{self.chunk_size}B\033[0m | "
                    f"总块数: \033[1;34m{self.total_frames}\033[0m | "
                    f"纠错: \033[1;34m{self.ecc_name}\033[0m | "
                    f"帧率: \033[1;35m{self.fps:.1f}FPS\033[0m\n"
                    f"广播帧: \033[1;37m#{frame_count}\033[0m  "
                    f"种子: \033[90m{seeds[0]}~{seeds[3]}\033[0m\n"
                )
                footer = "\n\033[90m请用手机或 Android 接收端对准屏幕扫码 | 按 Ctrl+C 退出\033[0m\n"

                sys.stdout.write(header + composed + footer)
                sys.stdout.flush()

                frame_count += 1
                time.sleep(self.interval_sec)

        except KeyboardInterrupt:
            pass
        finally:
            sys.stdout.write("\033[?25h\n\033[1;32m喷泉码广播已停止。\033[0m\n")
            sys.stdout.flush()

    # ── 标准模式主循环 ──

    def start(self):
        if self.fountain:
            self._start_fountain()
            return

        # 获取第一帧用于尺寸检测
        first_frame_idx = self.active_frame_indices[0]
        qr_content, qr_lines = self.get_frame(first_frame_idx)

        term_cols, term_lines = shutil.get_terminal_size((80, 24))
        needed_lines = qr_lines + 6
        if term_lines < needed_lines:
            auto_hint = (
                "\033[90m当前已启用 --auto 自适应，屏幕仍偏小，建议放大终端窗口或降低 -b 静区。\033[0m\n"
                if self.auto_size else
                "\033[90m建议全屏终端、或使用笔记本屏幕自适应模式: --auto\033[0m\n"
            )
            sys.stderr.write(
                f"\033[1;33m⚠️ 提示: 当前终端高度为 {term_lines} 行，而二维码展示需要约 {needed_lines} 行。\033[0m\n"
                f"{auto_hint}"
                f"3秒后自动开始播放...\n"
            )
            time.sleep(2)

        sys.stdout.write("\033[?25l\033[2J")
        sys.stdout.flush()

        seq_idx = 0
        total_active = len(self.active_frame_indices)

        if self.auto_size:
            term_cols, term_lines = shutil.get_terminal_size((80, 24))
            sys.stdout.write(
                f"\033[1;36m🖥 屏幕自适应: {term_cols}x{term_lines} 终端 → 自动切片 "
                f"\033[1;33m{self.chunk_size}B\033[0m \033[1;36m(共 {self.total_frames} 帧)\033[0m\n"
            )
            sys.stdout.flush()
            time.sleep(1.5)

        ratio_info = ""
        if self.is_compressed:
            saved_pct = (1.0 - (self.wire_size / self.orig_size)) * 100
            ratio_info = f"\033[1;32m[Gzip 压缩 -{saved_pct:.1f}%: {self.orig_size}B → {self.wire_size}B]\033[0m"

        try:
            while self.running:
                real_frame_idx = self.active_frame_indices[seq_idx]
                qr_content, _ = self.get_frame(real_frame_idx)

                mode_badge = (
                    f"\033[1;33m[定向补帧: 轮播 {total_active}/{self.total_frames} 帧]\033[0m"
                    if self.is_subset else
                    f"\033[1;32m[全量广播模式]\033[0m"
                )

                header = (
                    f"\033[H\033[1;36m📡 AirScan-QR CLI\033[0m {mode_badge} {ratio_info}\n"
                    f"文件: \033[1;32m{self.file_name}\033[0m | "
                    f"任务ID: \033[1;33m{self.task_id}\033[0m | "
                    f"切片: \033[1;34m{self.chunk_size}B\033[0m{' 🖥自适应' if self.auto_size else ''} | "
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
            sys.stdout.write("\033[?25h\n\033[1;32m广播已停止。\033[0m\n")
            sys.stdout.flush()


def main():
    parser = argparse.ArgumentParser(
        description="AirScan-QR CLI - 纯终端动态二维码文件传输广播工具"
    )
    parser.add_argument("file", help="要广播传输的文件路径")
    parser.add_argument("-s", "-c", "--chunk-size", "--size", dest="chunk_size", type=int, default=DEFAULT_CHUNK_SIZE,
                        help=f"单帧切片大小 (默认: {DEFAULT_CHUNK_SIZE}，适配标准 80x24 终端；喷泉码模式单位为原始字节)")
    parser.add_argument("-f", "--fps", type=float, default=DEFAULT_FPS,
                        help=f"广播帧率 FPS (默认: {DEFAULT_FPS})")
    parser.add_argument("-i", "--interval", type=float, default=None,
                        help="每帧间隔毫秒数 ms (例如: -i 50 即 50ms 一帧，相当于 20 FPS)")
    parser.add_argument("-e", "--ecc", "--error-correction", dest="ecc", type=str, default="L",
                        choices=["L", "M", "Q", "H", "l", "m", "q", "h"],
                        help="纠错等级: L(7%%), M(15%%), Q(25%%), H(30%%) (默认: L，切片数最少)")
    parser.add_argument("-b", "--border", type=int, default=DEFAULT_BORDER,
                        help=f"静区边框宽度 (默认: {DEFAULT_BORDER})")
    parser.add_argument("-F", "--frames", type=str, default=None,
                        help="仅显示/广播指定帧号（1-based，与手机端缺失帧编号完全一致，支持 '7,12,25' 或 '5-10' 等；喷泉码模式下无效）")
    parser.add_argument("-t", "--task-id", type=str, default=None,
                        help="指定 Task ID（若给手机补帧，必须指定为手机上显示的相同 Task ID，如 -t ABC）")
    parser.add_argument("--no-gzip", action="store_true",
                        help="禁用 Gzip 自动前置压缩（默认自动启用 Gzip）")
    parser.add_argument("--auto", action="store_true",
                        help="屏幕自适应切片：按当前终端（笔记本全屏）实际尺寸自动计算最大 chunk_size，忽略 -s")
    parser.add_argument("--fountain", action="store_true",
                        help=(
                            "喷泉码四象限模式[推荐]：同屏同时显示 4 个小二维码，"
                            "吞吐量最高可达单码模式的 4 倍。接收端须使用支持喷泉码的 Android 原生端或网页端。"
                        ))

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
            use_gzip=not args.no_gzip,
            auto_size=args.auto,
            fountain=args.fountain,
        )
        sender.start()
    except Exception as e:
        sys.stderr.write(f"\033[1;31m❌ 启动失败: {e}\033[0m\n")
        sys.exit(1)


if __name__ == "__main__":
    main()
