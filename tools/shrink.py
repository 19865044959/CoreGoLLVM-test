#!/usr/bin/env python3
"""UAF 失败最小化工具。

用法（在场景失败后）：
    python3 tools/shrink.py --scenario u01_wb_boundary_storm --profile soak \\
        --env "STRESS_SECONDS=86400 STRESS_WORKERS=8" [--toolchain stock|go] [--min-gc 100]

策略（三步，每步失败才继续缩小）：
  1. 时长二分：STRESS_SECONDS 从原值向下二分，找"仍失败的最短时长"；
  2. worker 削减：把 STRESS_WORKERS 逐级减半（至少 1），保留失败组合；
  3. 种子扫描：固定最小参数，重跑 N 个 seed 确认失败 seed 集合（偶发失败→提供 seed 列表）。

只做编排（调 run.sh/直接跑二进制），不改场景代码。每步输出一行结论，
最终打印最小复现命令与日志路径。判定"失败"= 进程退出码非 0 或无 STRESS PASS 行。
"""
import argparse
import os
import subprocess
import sys
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def run_once(scenario, profile, envs, toolchain, timeout_extra=180):
    """跑一次场景，返回 (ok, log_path)。"""
    d = os.path.join(ROOT, "stress", scenario)
    if not os.path.isdir(d):
        sys.exit(f"scenario not found: {d}")
    outdir = os.path.join(ROOT, "build", "shrink", scenario)
    os.makedirs(outdir, exist_ok=True)
    tag = f"shrink.{profile}.{'.'.join(k+v for k, v in sorted(envs.items()))}"
    log = os.path.join(outdir, tag + ".log")
    binp = build(d, toolchain, outdir, scenario)
    env = dict(os.environ)
    env["GOGC"] = "1"
    env["GOMAXPROCS"] = "2"
    env["GOMEMLIMIT"] = "256MiB"
    env["GODEBUG"] = "clobberfree=1,asyncpreemptoff=0"
    env["GOTRACEBACK"] = "crash"
    env.setdefault("UAF_SEED", "1")
    seconds = int(envs.get("STRESS_SECONDS", "0"))
    for k, v in envs.items():
        env[k] = v
    env["STRESS_CRASH_FILE"] = log + ".crash"
    try:
        with open(log, "wb") as f:
            rc = subprocess.call([binp], stdout=f, stderr=subprocess.STDOUT,
                                 env=env, cwd=outdir, timeout=seconds + timeout_extra)
    except subprocess.TimeoutExpired:
        return False, log
    if rc != 0:
        return False, log
    with open(log, "r", errors="replace") as f:
        content = f.read()
    return f"STRESS PASS {scenario}" in content, log


def build(d, toolchain, outdir, name):
    binp = os.path.join(outdir, name)
    if os.path.exists(binp):
        return binp
    files = sorted(f for f in os.listdir(d) if f.endswith(".go"))
    cmd = ["go", "build"]
    if toolchain == "go":
        cmd += ["-go-abi=gc"]
    cmd += ["-o", binp] + [os.path.join(d, f) for f in files]
    r = subprocess.run(cmd, capture_output=True, text=True, cwd=ROOT)
    if r.returncode != 0:
        sys.exit("build failed:\n" + r.stderr)
    return binp


def bisect_seconds(scenario, profile, envs, toolchain, hi):
    lo, best_fail = 0, None
    cur = dict(envs)
    # 先确认起点失败
    cur["STRESS_SECONDS"] = str(hi)
    ok, log = run_once(scenario, profile, cur, toolchain)
    if ok:
        print(f"[shrink] 起点未失败（STRESS_SECONDS={hi}），无需缩小")
        return None, None
    best_fail = (hi, log)
    while hi - lo > 1:
        mid = (lo + hi) // 2
        cur["STRESS_SECONDS"] = str(mid)
        ok, log = run_once(scenario, profile, cur, toolchain)
        print(f"[shrink] seconds={mid}: {'PASS' if ok else 'FAIL'}")
        if not ok:
            hi = mid
            best_fail = (mid, log)
        else:
            lo = mid
    return best_fail[0], best_fail[1]


def shrink_workers(scenario, profile, envs, toolchain):
    w = int(envs.get("STRESS_WORKERS", "4"))
    best = w
    while w > 1:
        w = max(1, w // 2)
        cur = dict(envs)
        cur["STRESS_WORKERS"] = str(w)
        ok, _ = run_once(scenario, profile, cur, toolchain)
        print(f"[shrink] workers={w}: {'PASS' if ok else 'FAIL'}")
        if not ok:
            best = w
    return best


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--scenario", required=True)
    ap.add_argument("--profile", default="soak")
    ap.add_argument("--env", default="", help='空格分隔的 K=V，如 "STRESS_SECONDS=86400 STRESS_WORKERS=8"')
    ap.add_argument("--toolchain", default="stock", choices=["stock", "go"])
    ap.add_argument("--seeds", type=int, default=8, help="最终参数下的 seed 扫描次数")
    args = ap.parse_args()

    envs = {}
    for kv in args.env.split():
        if "=" in kv:
            k, v = kv.split("=", 1)
            envs[k] = v
    hi = int(envs.get("STRESS_SECONDS", "86400"))

    print(f"[shrink] scenario={args.scenario} profile={args.profile} toolchain={args.toolchain} env={envs}")
    secs, log = bisect_seconds(args.scenario, args.profile, envs, args.toolchain, hi)
    if secs is None:
        sys.exit(0)
    envs["STRESS_SECONDS"] = str(secs)

    w = shrink_workers(args.scenario, args.profile, envs, args.toolchain)
    envs["STRESS_WORKERS"] = str(w)

    fails = []
    for s in range(1, args.seeds + 1):
        cur = dict(envs)
        cur["UAF_SEED"] = str(s)
        ok, slog = run_once(args.scenario, args.profile, cur, args.toolchain)
        print(f"[shrink] seed={s}: {'PASS' if ok else 'FAIL'}")
        if not ok:
            fails.append((s, slog))

    print()
    print("== 最小复现参数 ==")
    cmd_env = " ".join(f"{k}={v}" for k, v in sorted(envs.items()))
    print(f"  {cmd_env}  (失败 seed: {[s for s, _ in fails] or '任意'})")
    print(f"  复现命令示例：")
    print(f"    SUITE=stress FILTER={args.scenario} PROFILES={args.profile} TOOLCHAIN={args.toolchain} "
          f"STRESS_SECONDS={secs} STRESS_WORKERS={w} UAF_SEED={fails[0][0] if fails else 1} ./run.sh")
    if fails:
        print(f"  失败日志：{fails[0][1]}")


if __name__ == "__main__":
    main()
