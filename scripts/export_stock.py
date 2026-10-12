#!/usr/bin/env python3
"""导出单只（或全部）股票在系统内的全部属性，输出 JSON。

用法:
  python3 export_stock.py 601066                     # 单只，不含 K 线明细
  python3 export_stock.py 601066 --with-kline        # 附带全量日线
  python3 export_stock.py --all --out stocks.json    # 全市场（不含 K 线明细）
  python3 export_stock.py 601066 --out stock.json    # 输出到文件

数据库均为只读打开，不会写入。
"""
import argparse
import json
import os
import sqlite3
import sys

MAIN_DB = os.path.expanduser("~/.tongstock/cache/tongstock.db")
WATCHLIST_DB = os.path.expanduser("~/.tongstock/watchlist.db")
HISTORY_DB = os.path.expanduser("~/.tongstock/history.db")


def rows(db, sql, args=()):
    try:
        cur = db.execute(sql, args)
        cols = [d[0] for d in cur.description]
        return [dict(zip(cols, r)) for r in cur.fetchall()]
    except sqlite3.Error as e:
        return {"error": str(e)}


def parse_jsonish(v):
    if isinstance(v, str):
        try:
            return json.loads(v)
        except (ValueError, TypeError):
            return v
    return v


def export_one(main, wl, hist, code, with_kline):
    out = {"code": code}

    prof = rows(main, "SELECT * FROM stockinfo WHERE code=?", (code,))
    out["profile"] = prof[0] if prof else None

    def payload_row(sql):
        r = rows(main, sql, (code,))
        if isinstance(r, list) and r and isinstance(r[0].get("payload"), str):
            r[0]["payload"] = parse_jsonish(r[0]["payload"])
        return r[0] if r else None

    out["realtime_quote"] = payload_row(
        "SELECT payload, source_updated_at, updated_at FROM quote_snapshot WHERE code=?")

    out["finance"] = payload_row(
        "SELECT payload, source_updated_at, updated_at FROM finance_snapshot WHERE code=?")

    out["status_history"] = rows(
        main, "SELECT * FROM security_status_history WHERE code=? ORDER BY effective_from", (code,))

    out["kline_sync_state"] = rows(
        main, "SELECT * FROM kline_sync_state WHERE code=?", (code,))

    out["adjustment_factors"] = rows(
        main, "SELECT * FROM adjustment_factor WHERE code=? ORDER BY date", (code,))

    out["xdxr_events"] = rows(
        main, "SELECT * FROM xdxr_event WHERE code=? ORDER BY date", (code,))

    out["news"] = rows(
        main,
        """SELECT n.id, n.title, r.match_type, r.confidence
           FROM news_stock_ref r LEFT JOIN news_items n ON n.id = r.news_id
           WHERE r.code=? ORDER BY r.created_at DESC LIMIT 50""", (code,))

    out["hot_events"] = rows(
        main, "SELECT * FROM hot_events WHERE related_stocks LIKE ? ORDER BY created_at DESC LIMIT 20",
        (f"%{code}%",))

    out["latest_daily_selection"] = rows(
        main,
        """SELECT r.snapshot_date, r.status, c.rank, c.action, c.score, c.method_ids_json
           FROM daily_selection_candidate c JOIN daily_selection_run r ON r.run_id = c.run_id
           WHERE c.code=? ORDER BY r.created_at_ns DESC LIMIT 1""", (code,))

    out["quality_issues"] = rows(
        main, "SELECT * FROM quality_issue WHERE stock_code=? ORDER BY created_at DESC LIMIT 20", (code,))

    out["forward_signals_count"] = rows(
        main, "SELECT COUNT(*) AS n FROM forward_signal WHERE stock_code=?", (code,))

    if wl is not None:
        w = rows(wl, "SELECT * FROM watchlist WHERE code=?", (code,))
        out["watchlist"] = w[0] if w else None

    if hist is not None:
        h = rows(hist, "SELECT * FROM history_stocks WHERE code=?", (code,))
        out["analysis_history"] = h[0] if h else None

    if with_kline:
        out["kline_daily"] = rows(
            main, "SELECT date,open,high,low,close,volume,amount FROM kline WHERE code=? AND ktype=9 ORDER BY date", (code,))

    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("code", nargs="?", help="股票代码，如 601066")
    ap.add_argument("--all", action="store_true", help="导出全市场（不含 K 线明细）")
    ap.add_argument("--with-kline", action="store_true", help="单只导出时附带全量日线")
    ap.add_argument("--out", help="输出文件路径（默认 stdout）")
    args = ap.parse_args()

    if not args.all and not args.code:
        ap.error("需要股票代码或 --all")

    main_db = sqlite3.connect(f"file:{MAIN_DB}?mode=ro", uri=True)
    wl_db = sqlite3.connect(f"file:{WATCHLIST_DB}?mode=ro", uri=True) if os.path.exists(WATCHLIST_DB) else None
    hist_db = sqlite3.connect(f"file:{HISTORY_DB}?mode=ro", uri=True) if os.path.exists(HISTORY_DB) else None

    if args.all:
        codes = [r[0] for r in main_db.execute("SELECT code FROM stockinfo ORDER BY code")]
        data = [export_one(main_db, wl_db, hist_db, c, False) for c in codes]
    else:
        data = export_one(main_db, wl_db, hist_db, args.code, args.with_kline)

    text = json.dumps(data, ensure_ascii=False, indent=2, default=str)
    if args.out:
        with open(args.out, "w") as f:
            f.write(text)
        print(f"已写入 {args.out}")
    else:
        print(text)


if __name__ == "__main__":
    main()
