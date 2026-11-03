# Una sola tabla readings; temp_c guarda el Value de todas las metricas

Los seis Metric Type viven en una unica tabla `readings`, y la columna de valor conserva su nombre original `temp_c` (incluida la clave JSON en las respuestas de la API) aunque tambien guarda porcentajes, bytes y figuras de carga. La tabla es anterior al soporte multi-metrica; conservar el nombre evita romper el esquema y la API con datos ya en produccion, y agregar un Metric Type ahora solo requiere un nuevo valor de `metric_type`, sin migracion.

## Opciones consideradas

- **Una tabla por Metric Type**: esquema mas limpio, pero duplica la superficie de consultas/poda por tipo y rompe los dashboards y herramientas existentes.
- **Renombrar `temp_c` a `value`**: el nombre correcto, pero rompe la API JSON y los esquemas almacenados sin ganancia funcional. Diferido deliberadamente, no olvidado (ver el comentario en `db.go`).

## Consecuencias

- Los lectores no deben asumir que `temp_c` son grados Celsius: revisar `metric_type` y `unit`.
- Los cambios de esquema deben aplicarse en ambos dialectos de almacenamiento (ver ADR-0002).
