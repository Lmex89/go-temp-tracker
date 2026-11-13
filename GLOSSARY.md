# Sensor Temperature Tracker

Rastrea temperaturas del hardware y metricas del sistema (CPU, memoria, swap, disco, carga) en un host Linux, almacena las lecturas a lo largo del tiempo y senala anomalias de temperatura.

## Lenguaje

Los terminos se mantienen en ingles porque corresponden a identificadores del codigo; las definiciones explican el concepto.

### Medicion

**Sensor**:
Fuente de mediciones con nombre: un nucleo de CPU, una cifra de memoria, un punto de montaje o una ventana de carga.
_Evitar_: probe, canal, dispositivo

**Metric Type**:
Categoria a la que pertenece una medicion: temperature, cpu, memory, swap, disk o load.
_Evitar_: kind, serie, grupo

**Reading**:
Una medicion de un sensor en un instante puntual, con su Metric Type y su unidad.
_Evitar_: sample, data point, record

**Value**:
La cantidad medida de una Reading, expresada en la unidad de la Reading.
_Evitar_: temp (para cualquier cosa que no sea el Metric Type temperature)

### Estructura del codigo

**Store**:
Interfaz de persistencia (`Query`, `QueryLatest`, `Prune`, mas `Insert` en las implementaciones) con dos implementaciones, `SQLiteStore` y `PostgresStore`, en `internal/store`. `NewSQLiteStore(path)` recibe la ruta del archivo; `main.go` pasa `temps.db`.
_Evitar_: repositorio, DAO, backend

**Poller**:
Bucle que lee, inserta, poda y duerme; hay uno para temperatura (`internal/poller`, `Poller`) y uno por cada metrica del sistema (`RunMetricPoller`).
_Evitar_: worker, collector

**Internal package**:
Paquete bajo `internal/` (logging, timeutil, store, sensor, metrics, poller, httpapi, alerts); Go impide importarlo desde fuera del modulo (ver ADR-0003).
_Evitar_: pkg, libreria

### Deteccion de anomalias

**Baseline**:
La referencia estadistica por sensor (media, minimo, maximo, desviacion estandar) calculada sobre una ventana historica; la vara para medir lo que cuenta como anormal.
_Evitar_: rango normal, promedio (la media es un componente del Baseline)

**Deviation**:
Cuanto se eleva una Reading por encima de la media del Baseline de su sensor.
_Evitar_: delta, deriva, varianza

**Spike**:
Una Reading de temperatura cuyo Value excede la media del Baseline de su sensor en mas que el umbral de desviacion.
_Evitar_: anomalia, outlier, pico

**Severity**:
Clase de tamano de un Spike, segun cuanto su Deviation supera el umbral: mild, moderate, high o severe.
_Evitar_: nivel, grado, prioridad
