// kunci mengikuti konstanta Kind di internal/schemadiff/diff.go
const changeLabels: Record<string, string> = {
  create_schema: 'Buat schema',
  create_table: 'Buat tabel',
  add_column: 'Tambah kolom',
  alter_column_type: 'Ubah tipe kolom',
  alter_column_null: 'Ubah aturan NULL kolom',
  alter_column_default: 'Ubah nilai default kolom',
  drop_constraint: 'Hapus constraint',
  add_constraint: 'Tambah constraint',
  drop_index: 'Hapus index',
  create_index: 'Buat index',
  drop_column: 'Hapus kolom',
  drop_table: 'Hapus tabel',
}

// jenis yang belum dikenal ditampilkan apa adanya
export function changeLabel(kind: string) {
  return changeLabels[kind] ?? kind
}
