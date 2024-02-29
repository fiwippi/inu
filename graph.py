import json

import matplotlib.pyplot as plt
from matplotlib.figure import Figure

def parse_int_keys(d: dict) -> list[int]:
    return list(sorted(map(lambda s: int(s), d.keys())))

def graph_var(data: dict, symbol: str) -> Figure:
    fig, ax = plt.subplots(figsize=(10,6))
    # fig, ax = plt.subplots(figsize=(7.5,4.5))

    # Plot the variance
    for v in parse_int_keys(data):
        xs: list[float] = []
        ys: list[float] = []

        for mst in parse_int_keys(data[str(v)]):
            xs.append(mst)
            ys.append(data[str(v)][str(mst)])

        ax.plot(xs, ys, label=f"{symbol} = {v}", linestyle="--", marker="o")

    # Label the figure
    ax.set_title(f"Variance In {symbol}")
    ax.set_xlabel("Mean Success Rate (s)")
    ax.set_ylabel("Success Rate (%)")
    ax.legend()
    fig.tight_layout()
    
    return fig

if __name__ == "__main__":
    # Use the BMH style
    plt.style.use("bmh")

    # Load data
    with open("k-variance.json", "r") as f:
        k_variance = json.loads(f.read())
    with open("alpha-variance.json", "r") as f:
        alpha_variance = json.loads(f.read())

    # Graph variances
    graph_var(k_variance, "k",).savefig("k-variance.png", dpi=300)
    graph_var(alpha_variance, "α",).savefig("alpha-variance.png", dpi=300)


