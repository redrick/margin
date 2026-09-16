import json
import sys
from collections import defaultdict


def load(path):
    with open(path) as f:
        return json.load(f)


def summarize(entries):
    totals = defaultdict(int)
    for entry in entries:
        if entry["delta"] == 0:
            continue
        totals[entry["sku"]] += entry["delta"]
    return dict(sorted(totals.items()))


if __name__ == "__main__":
    for sku, total in summarize(load(sys.argv[1])).items():
        print(f"{sku}\t{total:+d}")
