import json
from statistics import fmean

import numpy as np
import matplotlib.pyplot as plt
from matplotlib.axes import Axes
from matplotlib.figure import Figure

# Parsing utilities for result data

def parse_int_keys(d: dict) -> list[int]:
    return list(sorted(map(lambda s: int(s), d.keys())))

# Figure manipulation

def new_figure(w: int = 10, h: int = 6) -> tuple[Figure, Axes]:
    return plt.subplots(figsize=(w,h))

def save_figure(name: str, fig: Figure, ax: Axes, legend_title=""):
    # Ensure we have a legend and tight layout enabled
    ax.legend(title=legend_title)
    fig.tight_layout()

    # Save the figure
    fig.savefig(name, dpi=300)

# Figures

def _draw_variance(name: str, symbol: str, key: str) -> tuple[Figure, Axes]:
    name = name.removesuffix(".json")
    with open(f"{name}.json", "r") as f:
        variance = json.loads(f.read())
   
    fig, ax = new_figure()

    for v in parse_int_keys(variance):
        xs: list[float] = []
        ys: list[float] = []

        msts = variance[str(v)]
        for x in parse_int_keys(msts):
            xs .append(x)
            ys.append(msts[str(x)][key])

        label = f"{v}"
        if symbol:
            label = f"{symbol} = {v}"
        ax.plot(xs, ys, label=label, linestyle="--", marker="o")

    ax.set_title(f"Variance In {symbol}")

    return fig, ax

def _draw_variance_sr(name: str, symbol: str):
    fig, ax = _draw_variance(name, symbol, "success-rate")
    ax.set_xlabel("Mean Session Time (s)")
    ax.set_ylabel("Success Rate (%)")
    name = name.removesuffix(".json")
    save_figure(f"{name}_success_rate", fig, ax)

def _draw_variance_mtl(name: str, symbol: str):
    fig, ax = _draw_variance(name, symbol, "mean-traffic-load")
    ax.set_xlabel("Mean Session Time (s)")
    ax.set_ylabel("Mean Traffic Load (bytes/s)")
    name = name.removesuffix(".json")
    save_figure(f"{name}_mean_traffic_load", fig, ax)

def draw_variance(name: str, symbol: str):
    _draw_variance_sr(name, symbol)
    _draw_variance_mtl(name, symbol)

def draw_dl_speed(name: str, profile: str):
    name = name.removesuffix(".json")
    with open(f"{name}.json", "r") as f:
        speed = json.loads(f.read())
   
    fig, ax = new_figure()

    for v in parse_int_keys(speed):
        xs: list[float] = []
        ys: list[float] = []
        errors: list[list[float]] = [[],[]]

        for x in parse_int_keys(speed[str(v)]):
            vs = [s/1e9 for s in speed[str(v)][str(x)]]

            xs .append(x)
            y = fmean(vs)
            ys.append(y)
            errors[0].append(y-min(vs))
            errors[1].append(max(vs)-y)

        ax.errorbar(xs, ys, yerr=errors, label=f"{v}", linestyle="--", marker="o", capsize=5)

    ax.set_title(f"Download Duration for Final Peer on {profile}")
    ax.set_xlabel("Number of Peers")
    ax.set_ylabel("Duration (s)")

    save_figure(name, fig, ax, legend_title="Filesize (MiB)")

def draw_routing_load(name: str):
    name = name.removesuffix(".json")
    with open(f"{name}.json", "r") as f:
        load = json.loads(f.read())

    fig, ax = new_figure()

    sizes = []
    speeds = {
        "Naive Routing": [],
        "IXP Avoidance": [],
    }

    to_mib = lambda b: (b / 1024) / 1024
    for v in parse_int_keys(load):
        sizes.append(v)

        midstream: list[float] = []
        downloader: list[float] = []
        for x in load[str(v)]:
            midstream.append(to_mib(x["midstream-dl"]))
            downloader.append(to_mib(x["downloader-dl"]))

        speeds["Naive Routing"].append(round(fmean(midstream), 1))
        speeds["IXP Avoidance"].append(round(fmean(downloader), 1))

    x = np.arange(len(sizes))
    width = 0.25
    multiplier = 0
    for attribute, measurement in speeds.items():
        offset = width * multiplier
        rects = ax.bar(x + offset, measurement, width, label=attribute)
        ax.bar_label(rects, padding=3)
        multiplier += 1

    ax.set_title("Ability Of DHT To Reduce Load At Different Filesizes")
    ax.set_xlabel("Filesize (MiB)")
    ax.set_xticks(x + width, sizes)
    ax.set_ylabel("TCP-Related Data Transferred (MiB)")

    save_figure(name, fig, ax)

if __name__ == "__main__":
    draw_variance("../results/k-variance.json", "k")
    draw_variance("../results/alpha-variance.json", "α")
    draw_dl_speed("../results/dl-speed-wifi.json", "Wi-Fi")
    draw_routing_load("../results/routing-load.json")

