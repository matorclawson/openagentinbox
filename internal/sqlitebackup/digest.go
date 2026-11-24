package sqlitebackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Digest returns a digest of a store's logical content: every row of every table, in any
// order. It does not depend on file layout, so two files holding the same rows agree and a
// single changed row does not. An upgrade compares it before and after the public start to
// learn whether the candidate accepted any write.
func Digest(ctx context.Context, path string) (string, error) {
	db, err := open(path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	names, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return "", err
	}
	var tables []string
	for names.Next() {
		var n string
		if err = names.Scan(&n); err != nil {
			names.Close()
			return "", err
		}
		tables = append(tables, n)
	}
	if err = names.Err(); err != nil {
		names.Close()
		return "", err
	}
	names.Close()
	h := sha256.New()
	for _, t := range tables {
		rows, err := db.QueryContext(ctx, `SELECT * FROM "`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		if err != nil {
			return "", err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		var sums []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				rows.Close()
				return "", err
			}
			r := sha256.New()
			for _, v := range vals {
				fmt.Fprintf(r, "%T:%v|", v, v)
			}
			sums = append(sums, string(r.Sum(nil)))
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
		sort.Strings(sums)
		fmt.Fprintf(h, "table %s %d\n", t, len(sums))
		for _, s := range sums {
			h.Write([]byte(s))
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
