package demo

import "time"

// entry is one article of a story: outlet id, delay in minutes since the
// first article, optional explicit title/summary/section and an optional
// stealth edit (Rev = new title, RA = minutes after first seen).
type entry struct {
	O   string
	D   int
	T   string
	S   string
	Sec string
	Rev string
	RA  int
}

// story is an invented event covered by several outlets. Today's stories
// (Ago > 0) are anchored relative to "now"; older ones (Ago == 0) to a local
// day offset and time of day, so every digest day gets its own stories.
type story struct {
	Key     string
	Name    string
	Ago     time.Duration // first article this long before now (recent stories)
	DayOff  int           // older stories: days before today (local)
	Hour    int
	Min     int
	Entries []entry
	Frames  map[string]string // leaning -> headline for entries without T
	Sum     string            // default summary
	Body    string            // paragraph appended to the content
	Frame   map[string]string // leaning -> dominant frame (annotation hint)
	Tone    map[string]string // leaning -> dominant tone (annotation hint)
}

const (
	lIzq  = "izquierda"
	lCIzq = "centro-izquierda"
	lCen  = "centro"
	lCDer = "centro-derecha"
	lDer  = "derecha"
)

var altTails = []string{"", ": las claves", ": lo que se sabe", ": todo lo que hay que saber"}

var stories = []story{
	{
		Key: "borrasca-ciro", Name: "Borrasca Ciro", Ago: 15 * time.Hour,
		Body:  "La Agencia Estatal de Meteorología mantiene el aviso máximo en la costa gallega y pide evitar los desplazamientos innecesarios. Los servicios de emergencias han atendido cientos de incidencias por caídas de árboles y cortes de suministro, y varios puertos permanecen cerrados.",
		Frame: map[string]string{lIzq: "responsabilidad", lCIzq: "interés humano", lCen: "interés humano", lCDer: "responsabilidad", lDer: "conflicto"},
		Tone:  map[string]string{lIzq: "crítico", lCIzq: "neutral", lCen: "neutral", lCDer: "neutral", lDer: "alarmista"},
		Entries: []entry{
			{O: "europapress", D: 0, T: "Alerta roja en Galicia por la borrasca Ciro, con rachas de hasta 130 km/h", S: "La Agencia Estatal de Meteorología activa el aviso máximo en la costa de A Coruña y Lugo desde las 18:00 horas y mantiene el naranja en el Cantábrico.", Sec: "nacional"},
			{O: "lasexta", D: 25, T: "Ciro arrasa la costa gallega: miles de hogares sin luz y vuelos cancelados", S: "El temporal deja cortes de suministro en decenas de municipios y obliga a cerrar puertos y a cancelar vuelos en Santiago y Vigo.", Sec: "españa",
				Rev: "Ciro golpea la costa gallega: cerca de 20.000 hogares sin luz y 12 vuelos cancelados", RA: 55},
			{O: "elpais", D: 38, T: "Galicia, en alerta roja por la borrasca Ciro: cierre de puertos, colegios cerrados y avisos en el Cantábrico", S: "Las autoridades piden evitar desplazamientos mientras el viento supera los 120 kilómetros por hora en el litoral.", Sec: "españa"},
			{O: "eldiario", D: 52, T: "La borrasca Ciro reabre el debate sobre los recortes en los servicios de emergencias", S: "Sindicatos de bomberos y personal de emergencias denuncian que la plantilla es insuficiente para atender un temporal de esta magnitud.", Sec: "portada",
				Rev: "La borrasca Ciro deja miles de hogares sin luz y reabre el debate sobre la prevención", RA: 70},
			{O: "elmundo", D: 70, T: "La borrasca Ciro deja a Galicia en alerta roja y pone a prueba los servicios de emergencia", S: "Los equipos de la Xunta y de la UME refuerzan el despliegue ante la previsión de nuevas rachas durante la noche.", Sec: "españa"},
			{O: "abc", D: 85, T: "Ciro golpea Galicia: vecinos denuncian que los avisos de la alerta llegaron tarde", S: "Residentes de la Costa da Morte aseguran que recibieron el mensaje de Protección Civil cuando el temporal ya había empezado.", Sec: "españa",
				Rev: "Ciro golpea Galicia: vecinos critican la tardanza de los avisos de la alerta", RA: 40},
			{O: "okdiario", D: 95, T: "Caos por Ciro: Galicia, sin luz y sin respuesta de las administraciones", S: "Calles cortadas, comercios cerrados y miles de familias a oscuras en una noche que se recordará en la comunidad.", Sec: "españa",
				Rev: "Caos por Ciro: Galicia, sin luz y con quejas por la respuesta de las administraciones", RA: 30},
			{O: "elconfidencial", D: 110, T: "Qué es una borrasca explosiva: la ciencia detrás de Ciro y por qué se intensificó en 24 horas", S: "Un descenso de presión de más de 24 hectopascales en un día convirtió la borrasca en un fenómeno de ciclogénesis explosiva.", Sec: "españa"},
			{O: "newtral", D: 130, T: "Es falso que la borrasca Ciro vaya a llegar a Madrid con vientos de 200 km/h: el bulo que circula por WhatsApp", S: "La AEMET no ha emitido ningún aviso para la capital con esa intensidad; las cifras del mensaje no coinciden con ningún modelo.", Sec: "verificación"},
			{O: "larazon", D: 150, T: "La Xunta y el Gobierno coordinan la respuesta a la borrasca Ciro en Galicia", S: "El comité de emergencias se reúne de forma permanente y mantiene abierto el canal con los ayuntamientos afectados.", Sec: "españa"},
			{O: "europapress", D: 190, T: "Retiran la alerta roja en Galicia, pero Ciro mantiene el aviso naranja en el Cantábrico", S: "La AEMET prevé que el temporal pierda intensidad a lo largo de la madrugada.", Sec: "nacional"},
			{O: "elpais", D: 175, T: "Una noche de temporal en la Costa da Morte: crónica de la borrasca Ciro", S: "Vecinos, bomberos y pescadores relatan cómo vivieron las horas de mayor intensidad del viento.", Sec: "españa"},
		},
	},
	{
		Key: "huelga-transporte", Name: "Huelga del transporte", Ago: 11 * time.Hour,
		Body:  "La convocatoria afecta al transporte de mercancías por carretera y a varias líneas de ferrocarril. Las empresas del sector calculan pérdidas diarias de cientos de millones de euros si el paro se mantiene más de una jornada.",
		Frame: map[string]string{lIzq: "conflicto", lCIzq: "conflicto", lCen: "económico", lCDer: "económico", lDer: "responsabilidad"},
		Tone:  map[string]string{lIzq: "crítico", lCIzq: "neutral", lCen: "neutral", lCDer: "alarmista", lDer: "crítico"},
		Entries: []entry{
			{O: "europapress", D: 0, T: "Los sindicatos convocan huelga en el transporte por carretera y ferrocarril el próximo lunes", S: "La Unión Sindical del Transporte reclama una subida salarial del 6% y una jornada de 37,5 horas semanales.", Sec: "economía"},
			{O: "eldiario", D: 14, T: "Los sindicatos mantienen la huelga del transporte tras fracasar la mediación: reclaman salarios dignos y menos horas", S: "La reunión con las patronales terminó sin acuerdo y el paro sigue convocado para el lunes.", Sec: "economía"},
			{O: "elpais", D: 25, T: "Huelga del transporte el lunes: los sindicatos rechazan la oferta de las patronales y mantienen el paro", S: "Las empresas ofrecían un 3,5% de subida en tres años, una cifra que los trabajadores consideran insuficiente.", Sec: "economía"},
			{O: "lavanguardia", D: 40, T: "Huelga del transporte el lunes: servicios mínimos del 60% y posibles retrasos en trenes y autobuses", S: "El Ministerio de Trabajo fija servicios mínimos para garantizar el abastecimiento y la movilidad básica.", Sec: "economía"},
			{O: "expansion", D: 55, T: "La huelga del transporte amenaza el abastecimiento: las empresas cifran las pérdidas en 300 millones al día", S: "Las patronales advierten de roturas de stock en supermercados y fábricas a partir de la segunda jornada.", Sec: "economía"},
			{O: "elmundo", D: 70, T: "Los sindicatos aprietan al Gobierno con una huelga del transporte que amenaza el abastecimiento", S: "El sector pide al Ejecutivo que medie para evitar un paro con impacto directo en la cadena logística.", Sec: "economía",
				Rev: "Los sindicatos convocan una huelga del transporte que amenaza el abastecimiento", RA: 35},
			{O: "larazon", D: 85, T: "Huelga del transporte: Trabajo fija servicios mínimos y las patronales alertan de desabastecimiento", S: "Los empresarios piden al Gobierno que garantice la libertad de circulación de los vehículos que no secunden el paro.", Sec: "economía"},
			{O: "abc", D: 100, T: "Huelga del transporte: el Gobierno, acusado de dejar a los ciudadanos como rehenes del paro", S: "La oposición reprocha al Ejecutivo su falta de iniciativa en la negociación entre sindicatos y empresas.", Sec: "economía",
				Rev: "Huelga del transporte: críticas al Gobierno por no evitar el paro", RA: 60},
			{O: "elconfidencial", D: 115, T: "Quién gana y quién pierde con la huelga del transporte: el mapa de las rutas afectadas", S: "Un análisis de las líneas de mercancías y viajeros que se verán más afectadas por la convocatoria.", Sec: "economía"},
			{O: "20minutos", D: 130, T: "Huelga del transporte el lunes: qué trenes y autobuses circularán y cómo pedir el reembolso", S: "Guía práctica con los servicios mínimos por comunidades y los derechos de los viajeros.", Sec: "economía"},
		},
	},
	{
		Key: "decreto-ayudas", Name: "Decreto de ayudas al alquiler", Ago: 9 * time.Hour,
		Body:  "El decreto prevé que las ayudas puedan solicitarse en las ventanillas autonómicas a partir del próximo mes. Los beneficiarios deberán acreditar unos ingresos máximos y un contrato de alquiler en vigor.",
		Frame: map[string]string{lIzq: "interés humano", lCIzq: "económico", lCen: "económico"},
		Tone:  map[string]string{lIzq: "laudatorio", lCIzq: "neutral", lCen: "neutral"},
		Entries: []entry{
			{O: "eldiario", D: 0, T: "El Gobierno aprueba el decreto de ayudas al alquiler: 250 euros al mes para jóvenes de hasta 35 años", S: "El Consejo de Ministros amplía el bono de alquiler joven a 80.000 personas más y lo prorroga dos años.", Sec: "política"},
			{O: "infolibre", D: 22, T: "Decreto de ayudas: el bono de alquiler joven llega a 80.000 nuevos beneficiarios", S: "La medida se financia con fondos europeos y con una partida adicional del Ministerio de Vivienda.", Sec: "portada"},
			{O: "elpais", D: 35, T: "El decreto de ayudas al alquiler amplía el bono joven y lo prorroga hasta 2028", S: "El texto eleva el umbral de ingresos y permite compatibilizar la ayuda con otras prestaciones autonómicas.", Sec: "españa"},
			{O: "lasexta", D: 48, T: "Estos son los requisitos del decreto de ayudas al alquiler para jóvenes: ingresos, edad y cómo pedirlo", S: "Guía con los pasos para solicitar el bono y los plazos que fijan las comunidades.", Sec: "españa"},
			{O: "elplural", D: 60, T: "Alivio para el alquiler joven: el decreto de ayudas llega a 80.000 personas más", S: "El Ejecutivo defiende que la medida mejora el acceso a la vivienda de la población joven.", Sec: "portada",
				Rev: "El decreto de ayudas al alquiler llega a 80.000 jóvenes más", RA: 45},
			{O: "elperiodico", D: 90, T: "El decreto de ayudas al alquiler beneficiará a jóvenes de hasta 35 años con 250 euros al mes", S: "Las comunidades gestionarán las solicitudes y los pagos se harán de forma mensual.", Sec: "política"},
		},
	},
	{
		Key: "tractorada", Name: "Tractorada contra la PAC", Ago: 7 * time.Hour,
		Body:  "La plataforma Campo Unido, que agrupa a asociaciones agrarias de varias comunidades, reclama que se retire el recorte previsto en las ayudas directas. La Delegación del Gobierno cifra la asistencia en miles de personas y cientos de tractores.",
		Frame: map[string]string{lCDer: "conflicto", lDer: "conflicto"},
		Tone:  map[string]string{lCDer: "crítico", lDer: "alarmista"},
		Entries: []entry{
			{O: "elmundo", D: 0, T: "Miles de agricultores colapsan el centro de Madrid con una tractorada contra el recorte de la PAC", S: "La protesta, convocada por la plataforma Campo Unido, ha cortado las principales arterias de acceso a la capital.", Sec: "españa"},
			{O: "okdiario", D: 15, T: "Tractorada en Madrid: el campo, harto de Bruselas y del Gobierno, bloquea la capital", S: "Cientos de tractores recorren el paseo de la Castellana en una protesta sin precedentes en los últimos años.", Sec: "españa",
				Rev: "Tractorada en Madrid: el campo, harto de la política agraria, bloquea la capital", RA: 50},
			{O: "abc", D: 28, T: "Tractorada contra el recorte de la PAC: «Bruselas ahoga al campo»", S: "Los manifestantes denuncian que el nuevo reparto de ayudas pone en riesgo la viabilidad de las explotaciones familiares.", Sec: "españa"},
			{O: "larazon", D: 40, T: "Los agricultores toman Madrid con tractores y exigen al Gobierno que frene el recorte de la PAC", S: "Los organizadores piden una reunión urgente con el Ministerio de Agricultura.", Sec: "españa"},
			{O: "libertaddigital", D: 55, T: "La tractorada contra la PAC paraliza Madrid y pone en jaque al Ministerio de Agricultura", S: "Los cortes de tráfico se prolongan durante buena parte de la mañana.", Sec: "portada"},
			{O: "eldebate", D: 70, T: "El campo grita basta: la tractorada llena Madrid contra la política agraria", S: "Agricultores y ganaderos advierten de que continuarán las movilizaciones si no hay respuesta.", Sec: "españa",
				Rev: "El campo se manifiesta: la tractorada llena Madrid contra la política agraria", RA: 25},
			{O: "theobjective", D: 90, T: "Quién está detrás de la tractorada: la plataforma Campo Unido y sus reclamaciones", S: "Un repaso a las asociaciones que integran la plataforma y a sus peticiones al Gobierno y a Bruselas.", Sec: "españa"},
		},
	},
	{
		Key: "seleccion", Name: "Selección nacional", Ago: 5 * time.Hour,
		Body:  "El seleccionador ha defendido su lista en rueda de prensa y ha asegurado que el grupo llega con la moral alta. La repesca se disputará en dos partidos, uno de ellos en casa.",
		Frame: map[string]string{lCen: "conflicto"},
		Entries: []entry{
			{O: "marca", D: 0, T: "Valdés cae lesionado y se pierde la repesca con la selección", S: "El delantero sufrió una molestia en el entrenamiento y causa baja en la lista de Montoya.", Sec: "deportes",
				Rev: "Valdés se pierde la repesca: lesión muscular de grado dos", RA: 70},
			{O: "europapress", D: 18, T: "Montoya convoca a 26 jugadores para la repesca: la gran novedad es la baja de Valdés", S: "El seleccionador incluye a tres debutantes en una lista marcada por las ausencias.", Sec: "nacional"},
			{O: "elpais", D: 33, T: "La selección llega a la repesca con una lista renovada y la baja de Iker Valdés", S: "Montoya apuesta por un equipo más joven, con tres jugadores que debutarán con la absoluta.", Sec: "españa"},
			{O: "elmundo", D: 45, T: "Montoya arriesga con la lista para la repesca: Valdés, baja por lesión", S: "El técnico renuncia a dos veteranos y confía en la rotación en el centro del campo.", Sec: "españa"},
			{O: "abc", D: 60, T: "La selección de Montoya, bajo presión: la repesca sin Valdés abre el debate sobre el relevo", S: "La ausencia del delantero obliga a reordenar el ataque en un momento delicado.", Sec: "españa"},
			{O: "lasexta", D: 75, T: "La selección española ya tiene lista para la repesca: Valdés, baja y tres debutantes", S: "Estos son los 26 convocados y los jugadores que se quedan fuera.", Sec: "españa"},
			{O: "elconfidencial", D: 95, T: "Así es la selección de Montoya: los números tras la lista para la repesca", S: "Edad media, minutos acumulados y clubes de procedencia de los convocados.", Sec: "españa"},
			{O: "20minutos", D: 110, T: "Lista de la selección para la repesca: los 26 convocados y las bajas", S: "Todos los nombres de la convocatoria y el calendario de los dos partidos.", Sec: "nacional"},
		},
	},
	// ---- older stories (they feed the previous days' digests and the baseline) ----
	{
		Key: "bolsa", Name: "Caída de la Bolsa", DayOff: 1, Hour: 12, Min: 10,
		Sum:   "El selectivo cierra la sesión con fuertes descensos en la banca y las eléctricas ante el temor a una subida de tipos.",
		Body:  "Los inversores han reducido su exposición a la renta variable tras conocerse unos datos de inflación superiores a lo esperado. La prima de riesgo se mantiene estable.",
		Frame: map[string]string{lIzq: "interés humano", lCIzq: "económico", lCen: "económico", lCDer: "económico", lDer: "responsabilidad"},
		Frames: map[string]string{
			lIzq:  "La caída de la Bolsa pone en alerta a los pequeños ahorradores: el Ibex pierde un 3,2%",
			lCIzq: "El Ibex 35 se desploma un 3,2% y arrastra a las bolsas europeas por el temor a una subida de tipos",
			lCen:  "Caída de la Bolsa: el Ibex 35 pierde un 3,2% en la peor sesión del año",
			lCDer: "La caída de la Bolsa sacude a la banca: el Ibex se deja un 3,2% y pierde los 11.500 puntos",
			lDer:  "Desplome de la Bolsa: el Ibex se hunde un 3,2% y el Gobierno, señalado por la incertidumbre",
		},
		Entries: []entry{
			{O: "expansion", D: 0, Sec: "economía", Rev: "El Ibex 35 cae un 3,2% y firma su peor sesión del año", RA: 30, T: "El Ibex 35 se desploma un 3,2% y firma su peor sesión del año en una caída de la Bolsa generalizada"},
			{O: "europapress", D: 15, Sec: "economía"}, {O: "elpais", D: 30, Sec: "economía"}, {O: "elmundo", D: 40, Sec: "economía"},
			{O: "abc", D: 55, Sec: "economía"}, {O: "lavanguardia", D: 65, Sec: "economía"}, {O: "eldiario", D: 80, Sec: "economía"},
			{O: "elconfidencial", D: 95, Sec: "economía"}, {O: "theobjective", D: 110, Sec: "portada"}, {O: "infolibre", D: 125, Sec: "portada"},
		},
	},
	{
		Key: "terremoto", Name: "Terremoto en el sureste", DayOff: 1, Hour: 18, Min: 40,
		Sum:   "El Instituto Geográfico Nacional sitúa el epicentro a diez kilómetros de profundidad y registra varias réplicas de menor intensidad.",
		Body:  "Los ayuntamientos han revisado edificios por precaución y los técnicos del Instituto Geográfico vigilan la actividad sísmica en la zona.",
		Frame: map[string]string{lIzq: "responsabilidad", lCIzq: "interés humano", lCen: "interés humano", lCDer: "interés humano", lDer: "responsabilidad"},
		Frames: map[string]string{
			lIzq:  "Un terremoto de magnitud 4,8 sacude el sureste peninsular y reabre el debate sobre la seguridad de los edificios antiguos",
			lCIzq: "Un terremoto de magnitud 4,8 sacude el sureste peninsular sin causar daños graves",
			lCen:  "Terremoto de magnitud 4,8 en el sureste: sin heridos y con daños leves en varios municipios",
			lCDer: "Un seísmo de 4,8 sacude el sureste peninsular y activa los protocolos de seguridad",
			lDer:  "Susto por un fuerte terremoto en el sureste: la gente sale a la calle y crecen las dudas sobre la respuesta oficial",
		},
		Entries: []entry{{O: "europapress", D: 0}, {O: "20minutos", D: 10}, {O: "elpais", D: 20}, {O: "lasexta", D: 30}, {O: "elmundo", D: 45}, {O: "eldiario", D: 60}, {O: "abc", D: 75}},
	},
	{
		Key: "vivienda", Name: "Reforma de la Ley de Vivienda", DayOff: 2, Hour: 11, Min: 0,
		Sum:   "El texto sale adelante tras semanas de negociación y deberá volver al Senado antes de su aprobación definitiva.",
		Body:  "La norma permite a las comunidades declarar zonas tensionadas y limitar las subidas del alquiler, y endurece las condiciones para los grandes tenedores.",
		Frame: map[string]string{lIzq: "interés humano", lCIzq: "conflicto", lCen: "conflicto", lCDer: "económico", lDer: "responsabilidad"},
		Frames: map[string]string{
			lIzq:  "El Congreso aprueba la reforma de la Ley de Vivienda: más protección para inquilinos en zonas tensionadas",
			lCIzq: "Luz verde a la reforma de la Ley de Vivienda: el Congreso limita las subidas del alquiler en zonas tensionadas",
			lCen:  "El Congreso aprueba la reforma de la Ley de Vivienda con 176 votos a favor",
			lCDer: "La reforma de la Ley de Vivienda sale adelante entre dudas de propietarios y comunidades por su aplicación",
			lDer:  "Aprobada la reforma de la Ley de Vivienda pese al rechazo de los propietarios: «ahuyentará la inversión»",
		},
		Entries: []entry{
			{O: "europapress", D: 0, Sec: "nacional"}, {O: "eldiario", D: 15, Sec: "política"}, {O: "elpais", D: 25, Sec: "españa"}, {O: "lavanguardia", D: 40, Sec: "política"},
			{O: "20minutos", D: 50, Sec: "nacional"}, {O: "elmundo", D: 60, Sec: "españa", Rev: "La reforma de la Ley de Vivienda sale adelante entre dudas de propietarios", RA: 45, T: "La reforma de la Ley de Vivienda sale adelante y amenaza a los pequeños propietarios"},
			{O: "abc", D: 75, Sec: "españa"}, {O: "larazon", D: 90, Sec: "españa"}, {O: "elconfidencial", D: 105, Sec: "españa"}, {O: "expansion", D: 120, Sec: "economía"},
		},
	},
	{
		Key: "incendio", Name: "Incendio en la sierra de Valdeolmos", DayOff: 2, Hour: 19, Min: 30,
		Sum:   "Los medios aéreos trabajan en la extinción de un fuego que ha obligado a desalojar a vecinos de tres pueblos.",
		Body:  "Las autoridades autonómicas han activado el nivel 2 del plan de emergencias y piden a los vecinos que sigan las instrucciones de Protección Civil.",
		Frame: map[string]string{lIzq: "interés humano", lCIzq: "interés humano", lCen: "interés humano", lCDer: "responsabilidad", lDer: "responsabilidad"},
		Frames: map[string]string{
			lIzq:  "El incendio de Valdeolmos obliga a desalojar a 900 vecinos y reabre el debate sobre la gestión forestal",
			lCIzq: "Un incendio forestal en la sierra de Valdeolmos obliga a desalojar a 900 vecinos",
			lCen:  "Incendio en la sierra de Valdeolmos: desalojados tres pueblos y 1.200 hectáreas afectadas",
			lCDer: "El incendio de Valdeolmos arrasa 1.200 hectáreas y obliga a desalojar tres localidades",
			lDer:  "Devastador incendio en Valdeolmos: tres pueblos desalojados y dudas sobre la prevención",
		},
		Entries: []entry{{O: "europapress", D: 0}, {O: "20minutos", D: 12}, {O: "lasexta", D: 25}, {O: "elpais", D: 40}, {O: "elmundo", D: 55}, {O: "abc", D: 70}, {O: "eldiario", D: 85}},
	},
	{
		Key: "ciberataque", Name: "Ciberataque a hospitales", DayOff: 3, Hour: 10, Min: 15,
		Sum:   "Los centros afectados han pasado a trabajar con papel mientras los técnicos restauran los sistemas.",
		Body:  "El Centro Criptológico Nacional investiga el origen del ataque. Los responsables sanitarios descartan, por ahora, que se hayan filtrado datos de pacientes.",
		Frame: map[string]string{lIzq: "responsabilidad", lCIzq: "responsabilidad", lCen: "interés humano", lCDer: "responsabilidad", lDer: "conflicto"},
		Frames: map[string]string{
			lIzq:  "Un ciberataque obliga a derivar urgencias en tres comunidades y expone la falta de inversión en ciberseguridad sanitaria",
			lCIzq: "Un ciberataque a la red hospitalaria obliga a derivar urgencias en tres comunidades",
			lCen:  "Ciberataque a hospitales: urgencias derivadas y citas canceladas en tres comunidades",
			lCDer: "Un ciberataque paraliza hospitales de tres comunidades y pone a prueba la ciberseguridad del sistema sanitario",
			lDer:  "Ataque informático a hospitales: caos en las urgencias y dudas sobre los datos de los pacientes",
		},
		Entries: []entry{{O: "europapress", D: 0}, {O: "elpais", D: 18}, {O: "elmundo", D: 30}, {O: "eldiario", D: 42}, {O: "20minutos", D: 55}, {O: "lavanguardia", D: 70}, {O: "abc", D: 85}, {O: "elconfidencial", D: 100}},
	},
	{
		Key: "lorente", Name: "Muere Andrés Lorente", DayOff: 3, Hour: 20, Min: 5,
		Sum:   "El cantautor, autor de varios discos fundamentales de la canción en español, murió en su casa rodeado de su familia.",
		Body:  "Su capilla ardiente se instalará mañana en un teatro de su ciudad natal. Compañeros de profesión y representantes políticos han expresado su pesar.",
		Frame: map[string]string{lIzq: "interés humano", lCIzq: "interés humano", lCen: "interés humano", lCDer: "interés humano", lDer: "interés humano"},
		Frames: map[string]string{
			lIzq:  "Muere Andrés Lorente, la voz que cantó a una generación, a los 78 años",
			lCIzq: "Muere el cantautor Andrés Lorente a los 78 años",
			lCen:  "Fallece Andrés Lorente, uno de los grandes cantautores de la música española",
			lCDer: "Adiós a Andrés Lorente, el cantautor que puso banda sonora a varias generaciones",
			lDer:  "Muere Andrés Lorente, el cantautor que marcó una época, a los 78 años",
		},
		Entries: []entry{{O: "elpais", D: 0, Sec: "portada"}, {O: "europapress", D: 8, Sec: "nacional"}, {O: "eldiario", D: 20, Sec: "portada"}, {O: "elmundo", D: 28, Sec: "españa"}, {O: "abc", D: 40, Sec: "españa"},
			{O: "20minutos", D: 52, Sec: "nacional"}, {O: "lavanguardia", D: 63, Sec: "portada"}, {O: "larazon", D: 75, Sec: "españa"}, {O: "elplural", D: 88, Sec: "portada"}, {O: "cope", D: 100, Sec: "portada"}},
	},
}
