package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Run is one analysis.
type Run struct {
	ID       int64
	NewDocID int64
	Title    string
	Status   string
	Stage    string
	Error    string
	// Verdict is the pre-review's conclusion: ready, clarify, reject or
	// empty for a run that drew none.
	Verdict   string
	Started   time.Time
	Finished  time.Time
	CreatedAt time.Time
}

// Evidence is one quote a finding cites.
type Evidence struct {
	DocID    int64    `json:"doc_id"`
	ChunkID  int64    `json:"chunk_id"`
	DocTitle string   `json:"doc_title"`
	Path     []string `json:"heading_path"`
	Quote    string   `json:"quote"`
	Date     string   `json:"doc_date,omitempty"`
}

// Span is where in the new document a finding points.
type Span struct {
	Quote  string   `json:"quote"`
	Path   []string `json:"heading_path"`
	Offset int      `json:"offset"`
}

// Finding is one review result, as PRD-FINAL §8.1 writes it.
type Finding struct {
	ID         int64
	RunID      int64
	Type       string
	Subtype    string
	Severity   string
	Confidence float64
	Title      string
	Detail     string
	Span       Span
	Evidence   []Evidence
	Question   string
	Fix        string
	Impact     string
	Status     string
	Note       string
	CreatedAt  time.Time
}

// FunctionPoint is a stable place in a product: a module, a function and
// the rule it is about.
type FunctionPoint struct {
	ID       int64
	Module   string
	Function string
	Topic    string
	Label    string
	// LatestRule is what this point's rule currently says, which is what
	// identifies it when two documents name it differently.
	LatestRule string
}

// ChangeRecord is one entry of a function point's ledger.
type ChangeRecord struct {
	ID       int64
	PointID  int64
	Label    string
	DocID    int64
	DocTitle string
	DocDate  time.Time
	// DocDateSource is where the document's date came from: an entry whose
	// document states no date of its own is not placed on the timeline, and
	// the row says so rather than showing the day it was read.
	DocDateSource string
	RunID         int64
	Kind          string
	Before        string
	After         string
	Impact        string
	Evidence      []Evidence
	Status        string
	Recorded      time.Time
	// Match provenance: how this entry's function point was identified
	// against the points already in the ledger. MatchKind is one of exact,
	// normalized, candidate, new; candidate means a similar point exists and
	// a person decides whether to merge, and MatchedPointID/MatchLabel name
	// that point.
	MatchKind      string
	MatchedPointID int64
	MatchLabel     string
	MatchScore     float32
}

// StartRun records an analysis and returns its id.
func (s *Store) StartRun(ctx context.Context, newDocID int64, title string, models map[string]any) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO analysis_runs (new_doc_id, title, models, status, current_stage, started_at)
		VALUES ($1,$2,$3,'running','start',now()) RETURNING id`,
		nullID(newDocID), title, JSON(models)).Scan(&id)
	return id, err
}

// FinishRun closes an analysis with its outcome.
func (s *Store) FinishRun(ctx context.Context, id int64, status, stage, errText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE analysis_runs SET status=$2, current_stage=$3, error=$4, finished_at=now() WHERE id=$1`,
		id, status, stage, nullText(errText))
	return err
}

// SetRunStage notes the stage a run reached, for the progress the window
// shows.
func (s *Store) SetRunStage(ctx context.Context, id int64, stage string, log map[string]any) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE analysis_runs
		SET current_stage=$2, stage_log = stage_log || $3::jsonb
		WHERE id=$1`, id, stage, JSON([]any{log}))
	return err
}

// SaveFindings writes a run's findings.
func (s *Store) SaveFindings(ctx context.Context, runID int64, findings []Finding) error {
	for _, f := range findings {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO findings (run_id, type, subtype, severity, confidence, title, detail,
				new_doc_span, evidence, question, suggested_fix, impact, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'open')`,
			runID, f.Type, nullText(f.Subtype), f.Severity, f.Confidence, f.Title, f.Detail,
			JSON(f.Span), JSON(f.Evidence), nullText(f.Question), nullText(f.Fix), nullText(f.Impact)); err != nil {
			return err
		}
	}
	return nil
}

// Findings reads a run's findings, most severe first.
func (s *Store) Findings(ctx context.Context, runID int64) ([]Finding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, type, coalesce(subtype,''), severity, confidence, title, detail,
		       new_doc_span, evidence, coalesce(question,''), coalesce(suggested_fix,''),
		       coalesce(impact,''), status, coalesce(reviewer_note,''), created_at
		FROM findings WHERE run_id = $1
		ORDER BY severity, confidence DESC`, runID)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanFinding)
}

// SetFindingReview records what a reviewer decided about a finding.
func (s *Store) SetFindingReview(ctx context.Context, id int64, status, note string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE findings SET status=$2, reviewer_note=$3, updated_at=now() WHERE id=$1`, id, status, nullText(note))
	return err
}

// ledgerOrder is how the ledger reads a function point's trail: the newest
// record of the newest document first. A document with no date of its own is
// never read as newer than one that states a date, and two documents of the
// same day are read from the older version to the newer one. Documents that
// tie in both are read in the order they were recorded.
const ledgerOrder = `
	ORDER BY (d.date_source IS NULL OR d.date_source = 'missing') ASC,
	         d.doc_date DESC,
	         coalesce(d.version_rank, -1) DESC,
	         c.recorded_at DESC, c.id DESC`

// FunctionPoints reads the registry a new document's function points are
// matched against.
func (s *Store) FunctionPoints(ctx context.Context) ([]FunctionPoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.module, f.function, f.topic, f.label,
		       coalesce((SELECT c.after_text FROM change_records c
		                 LEFT JOIN documents d ON d.id = c.doc_id
		                 WHERE c.function_point_id = f.id AND c.after_text IS NOT NULL`+
		ledgerOrder+` LIMIT 1), '')
		FROM function_points f ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (FunctionPoint, error) {
		var fp FunctionPoint
		err := rows.Scan(&fp.ID, &fp.Module, &fp.Function, &fp.Topic, &fp.Label, &fp.LatestRule)
		return fp, err
	})
}

// MergeFunctionPoints moves one point's ledger onto another, for the merge a
// person decides after a candidate match: history is repointed, never lost.
func (s *Store) MergeFunctionPoints(ctx context.Context, fromID, toID int64) (int, error) {
	if fromID == toID {
		return 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE change_records SET function_point_id=$2, match_kind='merge'
		WHERE function_point_id=$1`, fromID, toID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM function_points WHERE id=$1`, fromID); err != nil {
		return 0, err
	}
	// The merged history is re-read in order: what a point's rule was
	// becomes what it is, so the row that was this point's baseline can show
	// up as the change it turned out to be — with the rule it held before.
	if _, err := tx.Exec(ctx, `
		WITH ordered AS (
			SELECT c.id, lag(c.after_text) OVER (
				ORDER BY (d.date_source IS NULL OR d.date_source = 'missing'),
				         d.doc_date, coalesce(d.version_rank, -1), c.recorded_at, c.id) AS prev
			FROM change_records c
			LEFT JOIN documents d ON d.id = c.doc_id
			WHERE c.function_point_id = $1
		)
		UPDATE change_records c SET
			kind = CASE
				WHEN o.prev IS NULL OR o.prev = '' THEN 'baseline'
				WHEN o.prev = c.after_text THEN 'clarified'
				ELSE 'modified'
			END,
			before_text = CASE WHEN o.prev IS NULL OR o.prev = '' THEN NULL ELSE o.prev END
		FROM ordered o WHERE c.id = o.id`, toID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// matchKindOr keeps the column inside its vocabulary.
func matchKindOr(kind string) string {
	switch kind {
	case "exact", "normalized", "candidate", "merge", "new":
		return kind
	default:
		return "new"
	}
}

// UpsertFunctionPoint finds or creates a function point and returns it.
func (s *Store) UpsertFunctionPoint(ctx context.Context, fp FunctionPoint, firstDocID int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO function_points (module, function, topic, label, first_doc_id)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (module, function, topic) DO UPDATE SET updated_at = now()
		RETURNING id`,
		fp.Module, fp.Function, fp.Topic, fp.Label, nullID(firstDocID)).Scan(&id)
	return id, err
}

// SaveChangeRecord appends to a function point's ledger.
func (s *Store) SaveChangeRecord(ctx context.Context, rec ChangeRecord) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO change_records (function_point_id, doc_id, run_id, kind, before_text, after_text,
			impact, evidence, status, recorded_at, match_kind, matched_point_id, match_score)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, coalesce($10, now()), $11, $12, $13)
		RETURNING id`,
		rec.PointID, nullID(rec.DocID), nullID(rec.RunID), rec.Kind, nullText(rec.Before),
		nullText(rec.After), nullText(rec.Impact), JSON(rec.Evidence), rec.Status, nullTime(rec.Recorded),
		matchKindOr(rec.MatchKind), nullID(rec.MatchedPointID), rec.MatchScore).Scan(&id)
	return id, err
}

// LatestRecordBefore is a function point's newest record written before or on
// a document's own date: the rule the knowledge base held on the day this
// document was written, which is what the analysis compares against. A
// document with no date of its own takes the newest record there is.
func (s *Store) LatestRecordBefore(ctx context.Context, pointID int64, docDate time.Time, docDated bool) (ChangeRecord, bool, error) {
	var before any
	if docDated {
		before = nullDate(docDate)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.function_point_id, f.label, coalesce(c.doc_id,0), coalesce(d.title,''),
		       d.doc_date, coalesce(d.date_source,''), coalesce(c.run_id,0), c.kind, coalesce(c.before_text,''),
		       coalesce(c.after_text,''), coalesce(c.impact,''), c.evidence, c.status, c.recorded_at,
		       c.match_kind, coalesce(c.matched_point_id,0), coalesce(m.label,''), c.match_score
		FROM change_records c
		JOIN function_points f ON f.id = c.function_point_id
		LEFT JOIN function_points m ON m.id = c.matched_point_id
		LEFT JOIN documents d ON d.id = c.doc_id
		WHERE c.function_point_id = $1 AND c.status <> 'rejected'
		  AND ($2::date IS NULL OR (coalesce(d.date_source,'') <> 'missing' AND d.doc_date <= $2))`+
		ledgerOrder+` LIMIT 1`, pointID, before)
	if err != nil {
		return ChangeRecord{}, false, err
	}
	list, err := scanRows(rows, scanRecord)
	if err != nil || len(list) == 0 {
		return ChangeRecord{}, false, err
	}
	return list[0], true, nil
}

// ChangeRecords reads a function point's ledger, newest first, with the
// document each entry came from.
func (s *Store) ChangeRecords(ctx context.Context, pointID int64) ([]ChangeRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.function_point_id, f.label, coalesce(c.doc_id,0), coalesce(d.title,''),
		       d.doc_date, coalesce(d.date_source,''), coalesce(c.run_id,0), c.kind, coalesce(c.before_text,''),
		       coalesce(c.after_text,''), coalesce(c.impact,''), c.evidence, c.status, c.recorded_at,
		       c.match_kind, coalesce(c.matched_point_id,0), coalesce(m.label,''), c.match_score
		FROM change_records c
		JOIN function_points f ON f.id = c.function_point_id
		LEFT JOIN function_points m ON m.id = c.matched_point_id
		LEFT JOIN documents d ON d.id = c.doc_id
		WHERE c.function_point_id = $1`+ledgerOrder, pointID)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanRecord)
}

// Ledger reads every change record, newest first, for the report's ledger
// view.
func (s *Store) Ledger(ctx context.Context, limit int) ([]ChangeRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.function_point_id, f.label, coalesce(c.doc_id,0), coalesce(d.title,''),
		       d.doc_date, coalesce(d.date_source,''), coalesce(c.run_id,0), c.kind, coalesce(c.before_text,''),
		       coalesce(c.after_text,''), coalesce(c.impact,''), c.evidence, c.status, c.recorded_at,
		       c.match_kind, coalesce(c.matched_point_id,0), coalesce(m.label,''), c.match_score
		FROM change_records c
		JOIN function_points f ON f.id = c.function_point_id
		LEFT JOIN function_points m ON m.id = c.matched_point_id
		LEFT JOIN documents d ON d.id = c.doc_id`+
		ledgerOrder+` LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanRecord)
}

// RunLedger is the change ledger one run wrote: the report's own entries,
// rather than every record the knowledge base holds. A report's ledger that
// mixes other documents' records reads as if this run changed all of them.
func (s *Store) RunLedger(ctx context.Context, runID int64, limit int) ([]ChangeRecord, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.function_point_id, f.label, coalesce(c.doc_id,0), coalesce(d.title,''),
		       d.doc_date, coalesce(d.date_source,''), coalesce(c.run_id,0), c.kind, coalesce(c.before_text,''),
		       coalesce(c.after_text,''), coalesce(c.impact,''), c.evidence, c.status, c.recorded_at,
		       c.match_kind, coalesce(c.matched_point_id,0), coalesce(m.label,''), c.match_score
		FROM change_records c
		JOIN function_points f ON f.id = c.function_point_id
		LEFT JOIN function_points m ON m.id = c.matched_point_id
		LEFT JOIN documents d ON d.id = c.doc_id
		WHERE c.run_id = $1`+ledgerOrder+` LIMIT $2`, runID, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanRecord)
}

// ModuleTimeline is every change record of one module, oldest first: the
// order a reader follows a requirement's history in, so that what a new
// document says can be read against what the module already held. A module's
// name is matched as the documents spell it, which is why the comparison is
// exact: "会员" and "会员积分" are different modules.
func (s *Store) ModuleTimeline(ctx context.Context, module string, limit int) ([]ChangeRecord, error) {
	if limit <= 0 {
		limit = 400
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.function_point_id, f.label, coalesce(c.doc_id,0), coalesce(d.title,''),
		       d.doc_date, coalesce(d.date_source,''), coalesce(c.run_id,0), c.kind, coalesce(c.before_text,''),
		       coalesce(c.after_text,''), coalesce(c.impact,''), c.evidence, c.status, c.recorded_at,
		       c.match_kind, coalesce(c.matched_point_id,0), coalesce(m.label,''), c.match_score
		FROM change_records c
		JOIN function_points f ON f.id = c.function_point_id
		LEFT JOIN function_points m ON m.id = c.matched_point_id
		LEFT JOIN documents d ON d.id = c.doc_id
		WHERE f.module = $1
		ORDER BY (d.date_source IS NULL OR d.date_source = 'missing') ASC,
		         d.doc_date ASC, coalesce(d.version_rank, -1) ASC, c.recorded_at ASC, c.id ASC
		LIMIT $2`, module, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, scanRecord)
}

// ModuleDocuments lists the documents that stated something about a module,
// oldest first, with how many function points each contributed: what a module
// name is read against before its rules are opened one by one.
func (s *Store) ModuleDocuments(ctx context.Context, module string, limit int) ([]Doc, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.title, d.source_uri, d.format, d.doc_type, coalesce(d.product_line,''),
		       coalesce(d.module,''), coalesce(d.author,''), coalesce(d.version,''), d.status,
		       d.checksum, d.char_count, d.chunk_count, coalesce(d.embed_model,''),
		       d.doc_date, coalesce(d.date_source,''), d.date_confidence, coalesce(d.version_rank,-1),
		       d.parse_warnings, d.created_at
		FROM documents d
		WHERE d.id IN (SELECT c.doc_id FROM change_records c
		               JOIN function_points f ON f.id = c.function_point_id
		               WHERE f.module = $1 AND c.doc_id IS NOT NULL)
		ORDER BY (d.date_source IS NULL OR d.date_source = 'missing') ASC,
		         d.doc_date ASC, coalesce(d.version_rank, -1) ASC, d.id ASC
		LIMIT $2`, module, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (Doc, error) {
		var (
			d        Doc
			date     *time.Time
			warnings []byte
		)
		if err := rows.Scan(&d.ID, &d.Title, &d.SourceURI, &d.Format, &d.DocType, &d.ProductLine, &d.Module,
			&d.Author, &d.Version, &d.Status, &d.Checksum, &d.Chars, &d.Chunks, &d.EmbedModel, &date,
			&d.DateSource, &d.DateConf, &d.VersionRank, &warnings, &d.ImportedAt); err != nil {
			return d, err
		}
		d.Date = TimeOrZero(date)
		d.Warnings = decodeStrings(warnings)
		d.Title, d.Module, d.ProductLine = cleanText(d.Title), cleanText(d.Module), cleanText(d.ProductLine)
		return d, nil
	})
}

// ModuleNames lists the modules the ledger holds, with how many function
// points each has: the page that asks for a module name offers these.
func (s *Store) ModuleNames(ctx context.Context) ([]ModuleCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.module, count(DISTINCT f.id)::int
		FROM function_points f
		WHERE f.module <> ''
		GROUP BY f.module
		ORDER BY count(DISTINCT f.id) DESC, f.module ASC`)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (ModuleCount, error) {
		var m ModuleCount
		return m, rows.Scan(&m.Name, &m.Count)
	})
}

// SetChangeStatus records a review of a ledger entry.
func (s *Store) SetChangeStatus(ctx context.Context, id int64, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE change_records SET status=$2 WHERE id=$1`, id, status)
	return err
}

// Runs lists the analyses, newest first.
func (s *Store) Runs(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, coalesce(new_doc_id,0), coalesce(title,''), status, coalesce(current_stage,''),
		       coalesce(error,''), coalesce(pre_review->>'verdict',''), started_at, finished_at, created_at
		FROM analysis_runs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows, func(rows pgx.Rows) (Run, error) {
		var (
			r       Run
			started *time.Time
			finish  *time.Time
			newDoc  *int64
		)
		if err := rows.Scan(&r.ID, &newDoc, &r.Title, &r.Status, &r.Stage, &r.Error, &r.Verdict,
			&started, &finish, &r.CreatedAt); err != nil {
			return r, err
		}
		r.NewDocID = deref(newDoc)
		r.Started, r.Finished = TimeOrZero(started), TimeOrZero(finish)
		return r, nil
	})
}

// RunCounts is what the report list shows of a run without loading its
// findings: how many there are, how many are still open, the severity and
// type distribution, and how many history documents the run cited.
type RunCounts struct {
	Total    int
	Open     int
	Severity [4]int
	Types    map[string]int
	DocsHit  int
}

// RunCounts reads every run's finding counts in one query: the report list
// is a page of runs, and asking the database once per row would make opening
// it a hundred queries.
func (s *Store) RunCounts(ctx context.Context) (map[int64]RunCounts, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.run_id,
		       count(*)::int,
		       count(*) FILTER (WHERE f.status = 'open')::int,
		       count(*) FILTER (WHERE f.severity = 'S0')::int,
		       count(*) FILTER (WHERE f.severity = 'S1')::int,
		       count(*) FILTER (WHERE f.severity = 'S2')::int,
		       count(*) FILTER (WHERE f.severity = 'S3')::int,
		       count(*) FILTER (WHERE f.type = 'change_signal')::int,
		       count(*) FILTER (WHERE f.type = 'logic_gap')::int,
		       count(*) FILTER (WHERE f.type = 'ambiguity')::int,
		       count(*) FILTER (WHERE f.type = 'one_liner')::int,
		       count(*) FILTER (WHERE f.type = 'pre_review')::int,
		       (SELECT count(DISTINCT (e->>'doc_id'))
		        FROM findings f2, jsonb_array_elements(f2.evidence) e
		        WHERE f2.run_id = f.run_id AND e->>'doc_id' IS NOT NULL)::int
		FROM findings f
		GROUP BY f.run_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]RunCounts{}
	for rows.Next() {
		var (
			id                    int64
			c                     RunCounts
			change, gap, amb, one int
			pre                   int
		)
		if err := rows.Scan(&id, &c.Total, &c.Open, &c.Severity[0], &c.Severity[1], &c.Severity[2],
			&c.Severity[3], &change, &gap, &amb, &one, &pre, &c.DocsHit); err != nil {
			return nil, err
		}
		c.Types = map[string]int{
			"change_signal": change, "logic_gap": gap, "ambiguity": amb,
			"one_liner": one, "pre_review": pre,
		}
		out[id] = c
	}
	return out, rows.Err()
}

// StartJob records an ingest job.
func (s *Store) StartJob(ctx context.Context, root string, total int, parserVer, embedModel string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ingest_jobs (root, total, parser_ver, embed_model, status, started_at)
		VALUES ($1,$2,$3,$4,'running',now()) RETURNING id`, root, total, parserVer, embedModel).Scan(&id)
	return id, err
}

// UpdateJob records how far an ingest has come.
func (s *Store) UpdateJob(ctx context.Context, id int64, done int, failed []map[string]string, status string) error {
	finished := any(nil)
	if status != "running" {
		finished = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE ingest_jobs SET done=$2, failed=$3, status=$4, finished_at=coalesce($5, finished_at) WHERE id=$1`,
		id, done, JSON(failed), status, finished)
	return err
}

func scanFinding(rows pgx.Rows) (Finding, error) {
	var (
		f        Finding
		span     []byte
		evidence []byte
	)
	if err := rows.Scan(&f.ID, &f.RunID, &f.Type, &f.Subtype, &f.Severity, &f.Confidence, &f.Title, &f.Detail,
		&span, &evidence, &f.Question, &f.Fix, &f.Impact, &f.Status, &f.Note, &f.CreatedAt); err != nil {
		return f, err
	}
	json.Unmarshal(span, &f.Span)
	json.Unmarshal(evidence, &f.Evidence)
	f.Title, f.Detail = cleanText(f.Title), cleanText(f.Detail)
	f.Question, f.Fix, f.Impact = cleanText(f.Question), cleanText(f.Fix), cleanText(f.Impact)
	f.Note = cleanText(f.Note)
	f.Span.Quote, f.Span.Path = cleanText(f.Span.Quote), cleanPath(f.Span.Path)
	for i := range f.Evidence {
		f.Evidence[i].Quote = cleanText(f.Evidence[i].Quote)
		f.Evidence[i].DocTitle = cleanText(f.Evidence[i].DocTitle)
		f.Evidence[i].Path = cleanPath(f.Evidence[i].Path)
	}
	return f, nil
}

func scanRecord(rows pgx.Rows) (ChangeRecord, error) {
	var (
		rec      ChangeRecord
		date     *time.Time
		evidence []byte
	)
	if err := rows.Scan(&rec.ID, &rec.PointID, &rec.Label, &rec.DocID, &rec.DocTitle, &date, &rec.DocDateSource,
		&rec.RunID, &rec.Kind, &rec.Before, &rec.After, &rec.Impact, &evidence, &rec.Status, &rec.Recorded,
		&rec.MatchKind, &rec.MatchedPointID, &rec.MatchLabel, &rec.MatchScore); err != nil {
		return rec, err
	}
	rec.DocDate = TimeOrZero(date)
	json.Unmarshal(evidence, &rec.Evidence)
	rec.Before, rec.After, rec.Impact = cleanText(rec.Before), cleanText(rec.After), cleanText(rec.Impact)
	rec.Label, rec.DocTitle = cleanText(rec.Label), cleanText(rec.DocTitle)
	for i := range rec.Evidence {
		rec.Evidence[i].Quote = cleanText(rec.Evidence[i].Quote)
		rec.Evidence[i].DocTitle = cleanText(rec.Evidence[i].DocTitle)
		rec.Evidence[i].Path = cleanPath(rec.Evidence[i].Path)
	}
	return rec, nil
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// PreReview is what a run says about the document as a document: the verdict
// it would take into a review meeting, the risks behind it, and the questions
// a product manager has to answer before development can be scheduled. The
// findings carry the detail; this is the conclusion drawn from them.
type PreReview struct {
	// Verdict is ready, clarify or reject.
	Verdict string `json:"verdict"`
	// Summary says in a sentence or two why that verdict, with the numbers
	// behind it.
	Summary string `json:"summary"`
	// Risks are the biggest engineering risks, worst first.
	Risks []string `json:"risks"`
	// Asked is the question list, in the order it is asked.
	Asked []string `json:"asked"`
}

// SavePreReview records a run's conclusion.
func (s *Store) SavePreReview(ctx context.Context, runID int64, review PreReview) error {
	data, err := json.Marshal(review)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE analysis_runs SET pre_review = $2 WHERE id = $1`, runID, data)
	return err
}

// PreReview reads a run's conclusion, and whether it has one: a run from
// before the review existed answers with the empty object the column starts
// as, which is no conclusion rather than a verdict.
func (s *Store) PreReview(ctx context.Context, runID int64) (PreReview, bool, error) {
	var data []byte
	err := s.pool.QueryRow(ctx, `SELECT pre_review FROM analysis_runs WHERE id = $1`, runID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreReview{}, false, nil
	}
	if err != nil {
		return PreReview{}, false, err
	}
	var review PreReview
	if err := json.Unmarshal(data, &review); err != nil {
		return PreReview{}, false, nil
	}
	return review, review.Verdict != "", nil
}
