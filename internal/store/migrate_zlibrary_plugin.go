// s-library: Z-Library 插件迁移 —— 独立文件注册，与上游 migrate_*.go 惯例一致。
// 上游升级时本文件为纯新增，不产生合并冲突（docs/PLUGIN.md）。
package store

import "strings"

func init() {
	Migrations = append(Migrations, Migration{
		Version:     47,
		Description: "Add Z-Library download task, imported book mapping and settings tables",
		SQL: strings.Join([]string{
			`CREATE TABLE IF NOT EXISTS "ZlibraryDownloadTask" (
				"id"             INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
				"userId"         TEXT NOT NULL,
				"zbookId"        TEXT NOT NULL,
				"zbookHash"      TEXT NOT NULL,
				"title"          TEXT NOT NULL DEFAULT '',
				"author"         TEXT NOT NULL DEFAULT '',
				"ext"            TEXT NOT NULL DEFAULT '',
				"fileSize"       INTEGER NOT NULL DEFAULT 0,
				"libraryId"      TEXT NOT NULL DEFAULT '',
				"localBookId"    TEXT NOT NULL DEFAULT '',
				"status"         TEXT NOT NULL DEFAULT 'queued',
				"progress"       INTEGER NOT NULL DEFAULT 0,
				"stage"          TEXT NOT NULL DEFAULT 'fetch_info',
				"errorCode"      TEXT NOT NULL DEFAULT '',
				"errorMsg"       TEXT NOT NULL DEFAULT '',
				"quotaConsumed"  BOOLEAN NOT NULL DEFAULT 0,
				"retryCount"     INTEGER NOT NULL DEFAULT 0,
				"metaJson"       TEXT NOT NULL DEFAULT '{}',
				"createdAt"      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				"updatedAt"      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				"finishedAt"     DATETIME
			);`,
			`CREATE INDEX IF NOT EXISTS "ZlibraryDownloadTask_user_created_idx"
			 ON "ZlibraryDownloadTask"("userId", "createdAt" DESC);`,
			`CREATE INDEX IF NOT EXISTS "ZlibraryDownloadTask_status_idx"
			 ON "ZlibraryDownloadTask"("status");`,
			`CREATE TABLE IF NOT EXISTS "ZlibraryImportedBook" (
				"zbookId"      TEXT NOT NULL,
				"zbookHash"    TEXT NOT NULL,
				"localBookId"  TEXT NOT NULL DEFAULT '',
				"libraryId"    TEXT NOT NULL DEFAULT '',
				"importedBy"   TEXT NOT NULL,
				"importedAt"   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY ("zbookId", "zbookHash")
			);`,
			`CREATE INDEX IF NOT EXISTS "ZlibraryImportedBook_library_idx"
			 ON "ZlibraryImportedBook"("libraryId", "localBookId");`,
			`CREATE TABLE IF NOT EXISTS "ZlibrarySetting" (
				"key"        TEXT NOT NULL PRIMARY KEY,
				"value"      TEXT NOT NULL DEFAULT '',
				"updatedAt"  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			);`,
		}, "\n"),
	})
}
