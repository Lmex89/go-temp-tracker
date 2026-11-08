# Paquetes bajo internal/ en lugar de /pkg

El codigo Go se organiza en `internal/{logging,timeutil,store,sensor,metrics,poller,httpapi}`, con `main.go` solo para el cableado y los binarios adicionales en `cmd/`. Este proyecto es una aplicacion, no una biblioteca: nadie fuera del modulo debe importar estos paquetes, y `internal/` lo hace cumplir el compilador, de modo que podemos cambiar interfaces (por ejemplo `Store`) sin romper a terceros.

## Opciones consideradas

- **`/pkg`**: rechazada -- es solo una convencion que senala "codigo reutilizable", pero no impone nada y promete una API publica estable que no queremos mantener.
- **Todo en el paquete `main` (estado anterior)**: rechazada -- mezcla dominio, almacenamiento y HTTP, y `cmd/spike-report` y `cmd/migrate-to-postgres` no podian reutilizar nada sin duplicar codigo.

## Consecuencias

- Los binarios de `cmd/` pueden importar `sensors-temp/internal/...` porque viven en el mismo modulo.
- Los tests viven junto al codigo de cada paquete y usan archivos SQLite temporales (`t.TempDir()`), no el `temps.db` real.
- Si algun dia se necesita una API publica, se extrae un paquete concreto fuera de `internal/` mediante un nuevo ADR.
