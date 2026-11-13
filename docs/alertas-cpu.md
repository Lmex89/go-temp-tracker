# Alertas de CPU y temperatura por Telegram

Se pueden vigilar `cpu/total` y sensores térmicos seleccionados con avisos a un chat de Telegram. Esta versión no implementa correo Brevo. Funciona sin abrir el dashboard y guarda las Reading normalmente, sin cambiar el esquema ni la API.

## Configuracion

Sin `-alerts-config`, o con `enabled: false`, las alertas estan deshabilitadas y no se requieren credenciales. Para habilitarlas:

1. Copie `alerts.example.json` a `alerts.json` en la raiz del proyecto o a otra ruta privada fuera de `static/`. `alerts.json` en la raiz esta excluido de Git; cualquier otra ruta debe protegerse tambien del control de versiones. Nunca coloque destinos ni credenciales en recursos publicos.
2. Restrinja el archivo al usuario del servicio, por ejemplo `chmod 600 alerts.json`. Edite `enabled` a `true` y ponga el destino en `chat_id`. El ejemplo contiene tres reglas independientes: CPU al 50 % durante 3 minutos, CPU al 95 % durante 1 minuto y temperatura de `coretemp/Package id 0` a 65 °C durante 2 minutos.
3. Configure `TELEGRAM_BOT_TOKEN` en el entorno del proceso. No se cargan archivos `.env` automaticamente. El token no va en el JSON ni en argumentos de linea de comandos; evite incluirlo en historial de comandos, logs o archivos versionados.
4. Inicie el binario desde el directorio del proyecto con `./bin/temp-tracker -alerts-config alerts.json`. En systemd, configure la variable de entorno y el argumento en la unidad apropiada, protegiendo su configuracion; los scripts existentes no habilitan estas alertas. Reinicie el proceso para aplicar cambios.

Cada regla necesita `id` unico (letras ASCII, numeros, guion o guion bajo), `duration_minutes` positivo representable (puede ser decimal) y un umbral finito. Para `metric_type: "cpu"`, use `sensor: "cpu/total"` y `threshold` entre 0 y 100 inclusive. `cpu/total` es el promedio de los porcentajes de las CPU logicas. Para `metric_type: "temperature"`, seleccione el nombre exacto de un Sensor no vacío y un `threshold` en °C igual o superior a -273,15. Se admiten varias reglas por Sensor y diferentes sensores.

Para elegir un Sensor térmico disponible, consulte los nombres `sensor` en la respuesta local de `/api/current` mientras el monitor está funcionando; copie exactamente el nombre producido por el lector. La disponibilidad y los nombres dependen del hardware. El paquete CPU no equivale a sus núcleos ni a ACPI, y los filtros visuales del dashboard no determinan qué se captura ni qué se vigila. Los 65 °C del ejemplo son una elección operativa, no un límite de seguridad certificado del hardware.

Un archivo seleccionado que no pueda leerse o no sea JSON valido impide arrancar. Si esta habilitado, tambien se validan reglas, destino y token antes de abrir la base de datos. Una configuracion activa bajo `static/`, incluso mediante un enlace simbolico, se rechaza.

## Funcionamiento y limites

- Al habilitar alertas, CPU y temperatura usan una cadencia objetivo de 5 segundos entre inicios de ciclo, incluido el segundo bloqueante del lector de CPU. Esto prevalece sobre `CPU_POLL_INTERVAL` y `-interval`; las demas metricas y sus intervalos no cambian. Sin alertas se mantiene el comportamiento anterior. Los retrasos reales pueden superar la cadencia.
- Cada Reading igual o superior al umbral cuenta. La primera inicia el periodo pendiente; solo una Reading posterior que complete la duracion activa la regla. Una Reading inferior reinicia el periodo o, si estaba activa, produce una sola recuperacion inmediata. No hay recordatorios; otra activacion necesita un periodo completo nuevo.
- Una Reading ausente o no finita, fallo del lector o hueco superior a 10 segundos rompe la continuidad pendiente. Sin evidencia no se declara recuperacion. El tiempo dentro del proceso es monotono; no se garantiza continuidad entre Reading.
- El estado solo vive en memoria. Tras reiniciar no se acredita tiempo anterior ni se inventa una recuperacion, pero un incidente persistente puede notificarse otra vez despues de cumplir una duracion completa nueva.
- La cola de Telegram es FIFO, en memoria y limitada a 64 avisos pendientes; si se llena se registra la perdida sin bloquear la captura. Se intentan hasta tres envios por aviso, con espera entre intentos y respetando la indicacion de espera del proveedor. Cada solicitud tiene un timeout de 10 segundos y no sigue redirecciones. La aceptacion por Telegram no garantiza lectura ni entrega final.
- Los pendientes se pierden al reiniciar. Un fallo ambiguo tras la aceptacion puede causar duplicados al reintentar. No hay entrega garantizada ni cola persistente.
- CPU y temperatura pueden producir cada una aproximadamente 12 veces mas Reading que con el intervalo nominal anterior de 60 segundos. Vigile espacio y carga de la base; la retencion no se modifica automaticamente.
