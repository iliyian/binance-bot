package data

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/iliyian/binance-bot/binance"
	"github.com/iliyian/binance-bot/strategy"
	_ "modernc.org/sqlite"
)

// DB 封装了对 SQLite 数据库的操作
type DB struct {
	db *sql.DB
}

// NewDB 初始化数据库
func NewDB(dbPath string) (*DB, error) {
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 初始化表
	err = initSchema(db)
	if err != nil {
		return nil, err
	}

	return &DB{db: db}, nil
}

func initSchema(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS klines (
		symbol TEXT,
		interval TEXT,
		start_time INTEGER,
		open REAL,
		high REAL,
		low REAL,
		close REAL,
		volume REAL,
		PRIMARY KEY (symbol, interval, start_time)
	);
	`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("创建数据表失败: %w", err)
	}
	return nil
}

// Close 关闭数据库
func (d *DB) Close() error {
	return d.db.Close()
}

// InsertKlines 批量插入 K 线
func (d *DB) InsertKlines(klines []strategy.Kline) error {
	if len(klines) == 0 {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO klines (symbol, interval, start_time, open, high, low, close, volume) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, k := range klines {
		_, err = stmt.Exec(k.Symbol, k.Interval, k.StartTime, k.Open, k.High, k.Low, k.Close, k.Volume)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetKlines 获取指定交易对和周期、限制条数且 start_time <= maxTime 的 K 线
func (d *DB) GetKlines(symbol, interval string, limit int, maxTime int64) ([]strategy.Kline, error) {
	query := `
		SELECT start_time, open, high, low, close, volume 
		FROM klines 
		WHERE symbol = ? AND interval = ? AND start_time <= ? 
		ORDER BY start_time DESC 
		LIMIT ?
	`
	rows, err := d.db.Query(query, symbol, interval, maxTime, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var klines []strategy.Kline
	for rows.Next() {
		var k strategy.Kline
		k.Symbol = symbol
		k.Interval = interval
		if err := rows.Scan(&k.StartTime, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume); err != nil {
			return nil, err
		}
		klines = append(klines, k)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 查询出来是倒序的 (DESC)，需要翻转为正序 (时间从早到晚)
	for i, j := 0, len(klines)-1; i < j; i, j = i+1, j-1 {
		klines[i], klines[j] = klines[j], klines[i]
	}

	return klines, nil
}

// GetLatestKlineTime 获取数据库中某种 K 线的最新一条时间
func (d *DB) GetLatestKlineTime(symbol, interval string) (int64, error) {
	query := `SELECT MAX(start_time) FROM klines WHERE symbol = ? AND interval = ?`
	var maxTime sql.NullInt64
	err := d.db.QueryRow(query, symbol, interval).Scan(&maxTime)
	if err != nil {
		return 0, err
	}
	if !maxTime.Valid {
		return 0, nil // 表里还没有数据
	}
	return maxTime.Int64, nil
}

// CleanOldData 根据保留策略清理旧数据
func (d *DB) CleanOldData() error {
	// 1s 数据保留 30 天
	time30DaysAgo := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	_, err := d.db.Exec(`DELETE FROM klines WHERE interval = '1s' AND start_time < ?`, time30DaysAgo)
	if err != nil {
		return err
	}

	// 1m 数据保留 365 天
	time1YearAgo := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	_, err = d.db.Exec(`DELETE FROM klines WHERE interval = '1m' AND start_time < ?`, time1YearAgo)
	if err != nil {
		return err
	}

	// 1h 数据保留 3 年 (1095天)
	time3YearsAgo := time.Now().Add(-1095 * 24 * time.Hour).UnixMilli()
	_, err = d.db.Exec(`DELETE FROM klines WHERE interval = '1h' AND start_time < ?`, time3YearsAgo)
	if err != nil {
		return err
	}

	// 1d 数据保留 10 年 (3650天)
	time10YearsAgo := time.Now().Add(-3650 * 24 * time.Hour).UnixMilli()
	_, err = d.db.Exec(`DELETE FROM klines WHERE interval = '1d' AND start_time < ?`, time10YearsAgo)

	return err
}

// Backfill 自动回填断层数据
func (d *DB) Backfill(ctx context.Context, client *binance.Client, symbol, interval string, days int) error {
	startTime := time.Now().Add(time.Duration(-days*24) * time.Hour).UnixMilli()

	// 查一下库里该 interval 最新时间
	latestTime, err := d.GetLatestKlineTime(symbol, interval)
	if err != nil {
		return err
	}

	if latestTime > startTime {
		// 从数据库最新的一条时间开始拉取，避免重复
		startTime = latestTime
	}

	now := time.Now().UnixMilli()
	if startTime >= now {
		log.Printf("✅ %s %s 数据已是最新", symbol, interval)
		return nil
	}

	log.Printf("📥 开始回填 %s %s 数据, 起点: %v", symbol, interval, time.UnixMilli(startTime).Format(time.DateTime))

	// 分批拉取，每次 1000 条
	limit := 1000
	total := 0

	for {
		klines, err := client.GetKlines(ctx, symbol, interval, limit, startTime, 0)
		if err != nil {
			return err
		}

		if len(klines) == 0 {
			break
		}

		var sKlines []strategy.Kline
		for _, bk := range klines {
			open, _ := strconv.ParseFloat(bk.Open, 64)
			high, _ := strconv.ParseFloat(bk.High, 64)
			low, _ := strconv.ParseFloat(bk.Low, 64)
			closePrice, _ := strconv.ParseFloat(bk.Close, 64)
			vol, _ := strconv.ParseFloat(bk.Volume, 64)

			sKlines = append(sKlines, strategy.Kline{
				Symbol:    symbol,
				Interval:  interval,
				StartTime: bk.OpenTime,
				Open:      open,
				High:      high,
				Low:       low,
				Close:     closePrice,
				Volume:    vol,
			})
		}

		err = d.InsertKlines(sKlines)
		if err != nil {
			return err
		}

		total += len(sKlines)
		lastKlineTime := klines[len(klines)-1].OpenTime

		log.Printf("... 已拉取 %d 条 %s %s 数据, 最新时间: %s", total, symbol, interval, time.UnixMilli(lastKlineTime).Format(time.DateTime))

		// 币安 API 的结果包含了我们要的最新，如果获取不到 1000 条，说明到头了
		if len(klines) < limit {
			break
		}

		// 下一次查询的 startTime 是最后一条 kline 的时间 + 1 毫秒
		startTime = lastKlineTime + 1

		// 避免触发频控
		time.Sleep(200 * time.Millisecond)
	}

	log.Printf("✅ %s %s 数据回填完成, 共 %d 条", symbol, interval, total)
	return nil
}
