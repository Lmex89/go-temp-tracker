# Interfaz Store con backends SQLite y PostgreSQL

Las Reading pasan por una interfaz `Store` con dos implementaciones: `SQLiteStore` (por defecto, el archivo `temps.db`) y `PostgresStore` (usado por el servicio systemd). SQLite sigue siendo el valor por defecto para que las instalaciones existentes de un solo archivo sigan funcionando; PostgreSQL sostiene el servicio de larga duracion, y `cmd/migrate-to-postgres` copia un `temps.db` hacia adelante.

## Opciones consideradas

- **Solo SQLite**: rechazada -- seis pollers concurrentes contenden por el escritor unico (esperas de busy_timeout de 5s), y los escaneos de ultimo valor por sensor degradan conforme crece el historial.
- **Solo PostgreSQL**: rechazada -- dejaria varadas las instalaciones existentes de `temps.db` y el flujo de sqlite-backup-tool.

## Consecuencias

- El esquema vive en dos dialectos (auto-migracion en `db.go`, DDL completo en `pgstore.go`); cada cambio de esquema debe hacerse en ambos.
- SQLite fuerza una sola conexion (`SetMaxOpenConns(1)`); PostgreSQL conserva el pool por defecto.
