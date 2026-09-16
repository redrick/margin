import json
import sys


def load(path):
    with open(path) as f:
        return json.load(f)


def summarize(entries):
    totals = {}
    for entry in entries:
        totals[entry["sku"]] = totals.get(entry["sku"], 0) + entry["delta"]
    return totals


if __name__ == "__main__":
    for sku, total in summarize(load(sys.argv[1])).items():
        print(sku, total)
