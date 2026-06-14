package service

import (
	"errors"
	"io"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-common/backup/sqldump"
	commonV1 "github.com/go-tangra/go-tangra-common/gen/go/common/service/v1"

	"github.com/go-tangra/go-tangra-ipam/internal/data/ent/migrate"
)

// SqlBackupService implements the shared streaming common.service.v1.BackupService
// using the schema-agnostic sqldump engine over the module's own tables. It
// replaces the legacy per-field ipam.service.v1.BackupService (still registered
// during the transition).
type SqlBackupService struct {
	commonV1.UnimplementedBackupServiceServer

	log    *log.Helper
	engine *sqldump.Engine
}

// NewSqlBackupService builds the service. The table set comes from ent's
// generated migrate.Tables, so it always matches the live schema (drift-proof)
// and includes large tables that the legacy JSON export excluded.
func NewSqlBackupService(ctx *bootstrap.Context) *SqlBackupService {
	dsn := ctx.GetConfig().Data.Database.GetSource()

	tables := make([]string, 0, len(migrate.Tables))
	for _, t := range migrate.Tables {
		tables = append(tables, t.Name)
	}

	return &SqlBackupService{
		log:    ctx.NewLoggerHelper("ipam/service/sql-backup"),
		engine: sqldump.New(dsn, sqldump.Options{Module: "ipam", Tables: tables}),
	}
}

// ExportBackup streams the module's dump archive to the caller in chunks.
func (s *SqlBackupService) ExportBackup(req *commonV1.ExportBackupRequest, stream commonV1.BackupService_ExportBackupServer) error {
	w := &grpcExportWriter{stream: stream}
	// IPAM holds no out-of-Postgres secret material, so extras is nil.
	if err := s.engine.Dump(stream.Context(), w, nil); err != nil {
		s.log.Errorf("export backup: %v", err)
		return err
	}
	return w.flush()
}

// ImportBackup restores a dump archive streamed from the caller. The first
// message carries options; the rest carry archive chunks.
func (s *SqlBackupService) ImportBackup(stream commonV1.BackupService_ImportBackupServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	opts := first.GetOptions()
	if opts == nil {
		return errors.New("ipam: first ImportBackup message must carry options")
	}

	mode := sqldump.RestoreMerge
	if opts.GetMode() == commonV1.RestoreMode_RESTORE_MODE_FULL_SYNC {
		mode = sqldump.RestoreFullSync
	}

	res, _, err := s.engine.Restore(stream.Context(), &grpcImportReader{stream: stream}, mode)
	if err != nil {
		s.log.Errorf("import backup: %v", err)
		return stream.SendAndClose(&commonV1.ImportBackupResponse{
			Success: false, Module: "ipam", Warnings: []string{err.Error()},
		})
	}

	out := &commonV1.ImportBackupResponse{Success: true, Module: "ipam", Warnings: res.Warnings}
	for _, t := range res.Tables {
		out.Tables = append(out.Tables, &commonV1.TableResult{
			Table: t.Table, Rows: t.Rows, Deleted: t.Deleted, Skipped: t.Skipped, Note: t.Note,
		})
	}
	s.log.Infof("import backup done: %d tables, mode=%s", len(res.Tables), res.Mode)
	return stream.SendAndClose(out)
}

// grpcExportWriter adapts the server stream to io.Writer, coalescing the engine's
// many small framed writes into ~256 KB gRPC messages (the engine frames every
// COPY write, so without buffering a large table becomes hundreds of thousands of
// tiny messages). flush() must be called once at the end.
type grpcExportWriter struct {
	stream commonV1.BackupService_ExportBackupServer
	buf    []byte
}

const exportSendSize = 256 * 1024

func (w *grpcExportWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for len(w.buf) >= exportSendSize {
		if err := w.stream.Send(&commonV1.ExportBackupResponse{Content: w.buf[:exportSendSize]}); err != nil {
			return 0, err
		}
		// Drop the sent prefix; copy the remainder to a fresh slice so the sent
		// bytes aren't aliased by a later append.
		w.buf = append([]byte(nil), w.buf[exportSendSize:]...)
	}
	return len(p), nil
}

func (w *grpcExportWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	if err := w.stream.Send(&commonV1.ExportBackupResponse{Content: w.buf}); err != nil {
		return err
	}
	w.buf = nil
	return nil
}

// grpcImportReader adapts the client stream's content chunks to io.Reader.
type grpcImportReader struct {
	stream commonV1.BackupService_ImportBackupServer
	buf    []byte
	done   bool
}

func (r *grpcImportReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.done {
			return 0, io.EOF
		}
		msg, err := r.stream.Recv()
		if err == io.EOF {
			r.done = true
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		if c := msg.GetContent(); len(c) > 0 {
			r.buf = c
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
