# Alertas de CPU por Telegram

Este primer corte solo vigila `cpu/total` y envia avisos a un chat de Telegram. No implementa todavia alertas de temperatura ni correo Brevo. Funciona sin abrir el dashboard y guarda las Reading normalmente, sin cambiar el esquema ni la API.

## Configuracion

Sin `-alerts-config`, o con `enabled: false`, las alertas estan deshabilitadas y no se requieren credenciales. Para habilitarlas:

1. Copie `alerts.example.json` a `alerts.json` en la raiz del proyecto o a otra ruta privada fuera de `static/`. `alerts.json` en la raiz esta excluido de Git; cualquier otra ruta debe protegerse tambien del control de versiones. Nunca coloque destinos ni credenciales en recursos publicos.
2. Restrinja el archivo al usuario del servicio, por ejemplo `chmod 600 alerts.json`. Edite `enabled` a `true` y ponga el destino en `chat_id`. El ejemplo contiene dos reglas independientes: 50 % durante 3 minutos y 95 % durante 1 minuto.
3. Configure `TELEGRAM_BOT_TOKEN` en el entorno del proceso. No se cargan archivos `.env` automaticamente. El token no va en el JSON ni en argumentos de linea de comandos; evite incluirlo en historial de comandos, logs o archivos versionados.
4. Inicie el binario desde el directorio del proyecto con `./bin/temp-tracker -alerts-config alerts.json`. En systemd, configure la variable de entorno y el argumento en la unidad apropiada, protegiendo su configuracion; los scripts existentes no habilitan estas alertas. Reinicie el proceso para aplicar cambios.

Cada regla necesita `id` unico (letras ASCII, numeros, guion o guion bajo), `metric_type: "cpu"`, `sensor: "cpu/total"`, `threshold` entre 0 y 100 inclusive y `duration_minutes` positivo, que puede ser decimal. `cpu/total` es el promedio de los porcentajes de las CPU logicas. Un archivo seleccionado que no pueda leerse o no sea JSON valido impide arrancar. Si esta habilitado, tambien se validan reglas, destino y token antes de abrir la base de datos. Una configuracion activa bajo `static/`, incluso mediante un enlace simbolico, se rechaza.

## Funcionamiento y limites

- Al habilitar alertas, CPU usa una cadencia objetivo de 5 segundos entre inicios de ciclo, incluido el segundo bloqueante del lector. Esto prevalece sobre `CPU_POLL_INTERVAL`; las demas metricas y sus intervalos no cambian. Sin alertas se mantiene el comportamiento anterior. Los retrasos reales pueden superar la cadencia.
- Cada Reading igual o superior al umbral cuenta. La primera inicia el periodo pendiente; solo una Reading posterior que complete la duracion activa la regla. Una Reading inferior reinicia el periodo o, si estaba activa, produce una sola recuperacion inmediata. No hay recordatorios; otra activacion necesita un periodo completo nuevo.
- Una Reading ausente o no finita, fallo del lector o hueco superior a 10 segundos rompe la continuidad pendiente. Sin evidencia no se declara recuperacion. El tiempo dentro del proceso es monotono; no se garantiza continuidad entre Reading.
- El estado solo vive en memoria. Tras reiniciar no se acredita tiempo anterior ni se inventa una recuperacion, pero un incidente persistente puede notificarse otra vez despues de cumplir una duracion completa nueva.
- La cola de Telegram es FIFO, en memoria y limitada a 64 avisos pendientes; si se llena se registra la perdida sin bloquear la captura. Se intentan hasta tres envios por aviso, con espera entre intentos y respetando la indicacion de espera del proveedor. Cada solicitud tiene un timeout de 10 segundos y no sigue redirecciones. La aceptacion por Telegram no garantiza lectura ni entrega final.
- Los pendientes se pierden al reiniciar. Un fallo ambiguo tras la aceptacion puede causar duplicados al reintentar. No hay entrega garantizada ni cola persistente.
- CPU puede producir aproximadamente 12 veces mas Reading que con el intervalo nominal anterior de 60 segundos. Vigile espacio y carga de la base; la retencion no se modifica automaticamente.
