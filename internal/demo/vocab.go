package demo

import "strings"

// Background chatter: every bucket is a set of title patterns with {slot}
// placeholders and a list of values per slot. All names, clubs and companies
// are invented. Values are drawn from shuffled "decks" (no repetition until
// the deck is exhausted) so that two outlets rarely end up with the same
// background headline within a day or two (which would look like a story).
type bucket struct {
	patterns []string
	fills    map[string][]string
	sums     []string // generic follow-up sentences for the summary
}

var buckets = map[string]*bucket{
	"politica": {
		patterns: []string{
			"{act} {verb} {obj}",
			"{act} {verb} {obj} {tail}",
			"{act} {verb} {obj}, {tail}",
			"{act} {verb} {obj} con una dotación de {n} millones",
			"Los grupos piden que {act_l} {verb2} {obj}",
			"{act} {verb} {obj} y abre un periodo de consultas",
		},
		fills: map[string][]string{
			"act":   {"El Gobierno", "El Congreso", "El Senado", "El Consejo de Ministros", "La oposición", "El Ejecutivo", "El Ministerio de Hacienda", "El Ministerio de Sanidad", "El Ministerio de Educación", "El Ministerio de Transportes", "La Junta de Andalucía", "La Generalitat", "El Gobierno vasco", "La Comunidad de Madrid", "El Tribunal Supremo", "La Fiscalía General"},
			"act_l": {"el Gobierno", "el Ejecutivo", "la Mesa del Congreso", "el Ministerio de Hacienda", "el Consejo de Ministros", "la Junta de Portavoces"},
			"verb":  {"aprobará", "prepara", "negocia", "estudia", "defiende", "revisa", "pospone", "impulsa", "rechaza", "aplaza", "presenta", "anuncia", "retoma", "somete a debate"},
			"verb2": {"retire", "aclare", "explique", "negocie", "revise", "aplace", "detalle"},
			"obj": {"la reforma de las pensiones mínimas", "el plan de becas universitarias", "la nueva ley de protección de datos", "el reglamento de la ley de dependencia", "los presupuestos de 2027",
				"la financiación autonómica", "la ampliación del permiso de paternidad", "la tarjeta sanitaria única", "el pacto por la educación", "la digitalización de la Justicia",
				"la ley de familias", "el estatuto del becario", "la reforma del reglamento del Congreso", "el plan de ciberseguridad de las administraciones", "la ley de servicios de atención a la clientela",
				"el nuevo mapa judicial", "la regulación de las viviendas turísticas", "el plan de empleo juvenil", "la reducción del IVA de los alimentos básicos", "el reparto de los fondos europeos",
				"el calendario de la reforma fiscal", "la ley de bosques y gestión forestal", "el plan de lucha contra la despoblación", "el protocolo de salud mental en las escuelas", "la ley de industria",
				"el programa de eficiencia energética en edificios públicos", "la reforma de la ley de contratos públicos", "el decreto de transparencia en las listas de espera", "la estrategia nacional del hidrógeno verde", "el plan de rehabilitación de colegios",
				"el sistema de cita previa en la Administración", "la ley del cine y el audiovisual", "la reforma del Estatuto de los Trabajadores", "la política de visados para profesionales", "el plan de choque contra las listas de espera",
				"la ley de universidades", "el fondo de rescate de los ayuntamientos", "la nueva tarifa del transporte público", "la regulación de las plataformas de reparto", "el plan de carreteras secundarias",
				"la ley de movilidad sostenible", "el abono único de transporte", "la oficina antifraude", "la reforma de la Ley de Vivienda", "el pacto de Estado contra la violencia machista",
				"el programa de ayudas a la natalidad", "la ley de eficiencia de la Justicia", "la tasa a las grandes plataformas digitales", "el plan de renovación de la flota de autobuses", "la ley de cambio climático autonómica"},
			"tail": {"tras meses de negociación", "pese a las críticas de la oposición", "con el visto bueno de las comunidades", "antes de que acabe el año", "en el próximo pleno", "según fuentes parlamentarias", "sin el apoyo de sus socios habituales", "a la espera de un informe jurídico", "tras la reunión de esta semana", "con cambios respecto al borrador"},
		},
		sums: []string{
			"La medida deberá pasar todavía por el trámite parlamentario antes de su entrada en vigor.",
			"Los portavoces consultados reconocen que la negociación sigue abierta.",
			"El texto incorpora cambios respecto al borrador que se conoció el mes pasado.",
			"Fuentes del departamento aseguran que el calendario se cumplirá, aunque admiten dificultades.",
			"Las comunidades autónomas reclaman más margen para aplicar la norma en sus territorios.",
			"Los expertos advierten de que el impacto real dependerá del desarrollo reglamentario.",
			"La iniciativa llega después de varios meses de contactos entre los grupos.",
		},
	},
	"economia": {
		patterns: []string{
			"El Ibex 35 cierra la sesión con una {mov} del {pct}% y {sector} como protagonista",
			"La inflación se {infl} al {pct}% en {mes}, según el INE",
			"El paro registrado {paro} en {big} personas en {mes}",
			"El euríbor {eur} al {pct}% y {hip} las hipotecas variables",
			"{emp} {empv} un {n}% su beneficio en los nueve primeros meses",
			"El precio de la luz {luz} mañana: las horas más baratas serán {hrs}",
			"Hacienda {hac} {fisc}",
			"El Banco de España {bde}",
			"{sector_c} {sec_v}: {sec_t}",
			"La cesta de la compra {cesta} un {pct}% en {mes} y {cesta_t}",
			"{emp} anuncia {ope}",
			"Las exportaciones españolas {exp} en {mes} y {exp_t}",
		},
		fills: map[string][]string{
			"mov":      {"subida", "bajada"},
			"sector":   {"la banca", "las eléctricas", "las constructoras", "el sector turístico", "las tecnológicas", "las aerolíneas", "la distribución"},
			"infl":     {"modera", "acelera", "estabiliza"},
			"mes":      {"octubre", "septiembre", "agosto", "noviembre"},
			"paro":     {"baja", "sube"},
			"eur":      {"baja", "sube", "se mantiene"},
			"hip":      {"abarata", "encarece", "deja sin cambios"},
			"emp":      {"Iberluz", "Naviera Cantábrica", "Grupo Alcázar", "Banco Levante", "Aceros del Norte", "Textil Marfil", "Altamar Energía", "Vinícola del Duero", "Constructora Meseta", "Cementos Alcalá", "Logística Tajo", "Alimentaria Sierra"},
			"empv":     {"aumenta", "reduce", "multiplica", "recorta"},
			"luz":      {"sube", "baja", "se dispara", "se abarata"},
			"hrs":      {"de 14:00 a 16:00", "de 03:00 a 06:00", "las del mediodía", "las de la madrugada"},
			"hac":      {"abre una consulta sobre", "recuerda el plazo de", "adelanta el calendario de", "simplifica los trámites de"},
			"fisc":     {"la declaración de patrimonio", "las devoluciones del IRPF", "el modelo 347", "las deducciones por obras de eficiencia", "los pagos fraccionados de autónomos", "la declaración de las rentas del alquiler", "el pago del impuesto de circulación", "las ayudas fiscales a la rehabilitación", "el calendario de la campaña de la renta"},
			"bde":      {"eleva su previsión de crecimiento para el próximo año", "alerta del endeudamiento de los hogares", "mantiene su estimación de inflación", "ve riesgos en el mercado inmobiliario", "pide mejorar la productividad", "avisa de que el crédito al consumo crece por encima de los salarios", "pide cautela con los precios de la vivienda de segunda mano", "prevé una desaceleración del empleo en los servicios", "publica un informe sobre el ahorro de los hogares"},
			"sector_c": {"El turismo", "La industria", "La construcción", "El comercio minorista", "La agricultura", "El sector del automóvil", "La hostelería"},
			"sec_v":    {"modera su ritmo", "mantiene el pulso", "recupera terreno", "pide ayudas"},
			"sec_t":    {"los datos del trimestre", "lo que dicen los empresarios", "las claves para el invierno", "qué esperan los analistas"},
			"cesta":    {"sube", "baja", "se encarece", "se abarata"},
			"cesta_t":  {"el aceite sigue en el centro del debate", "las frutas lideran las subidas", "los lácteos moderan sus precios", "los huevos vuelven a disparar el índice"},
			"ope":      {"un plan de inversión de {n} millones", "una ampliación de capital", "la apertura de una nueva planta en Castilla", "un expediente de regulación temporal", "el lanzamiento de una oferta de recompra", "un acuerdo con un socio tecnológico"},
			"exp":      {"crecen", "caen", "se estancan"},
			"exp_t":    {"el déficit comercial se reduce", "el déficit comercial se amplía", "el superávit con Europa resiste"},
		},
		sums: []string{
			"Los analistas consultados esperan que la tendencia se mantenga durante las próximas semanas.",
			"El dato se conoce un día después de que varios organismos revisaran sus previsiones.",
			"Los mercados reaccionaron con cautela a la publicación de las cifras.",
			"El sector pide estabilidad regulatoria para planificar las inversiones del próximo ejercicio.",
			"La evolución depende, según los expertos, del comportamiento del consumo y de los tipos de interés.",
		},
	},
	"sociedad": {
		patterns: []string{
			"El número de {salud} crece un {pct}% en {ccaa}",
			"Un estudio de {univ} concluye que {hallazgo}",
			"{ciudad} estrena {obra}",
			"Detenidas {n} personas en {ciudad} por {delito}",
			"{org} alerta de {alerta}",
			"{ccaa} ampliará {amp} el próximo curso",
			"Así afecta {tema} a {colectivo}: lo que dicen los datos",
			"Cinco claves para entender {tema}",
		},
		fills: map[string][]string{
			"salud":     {"casos de gripe", "consultas por ansiedad", "urgencias pediátricas", "denuncias por estafa telefónica", "accidentes en carreteras secundarias", "personas que viven solas", "reclamaciones contra aerolíneas", "peticiones de teletrabajo"},
			"ccaa":      {"Andalucía", "Aragón", "Asturias", "Castilla y León", "Galicia", "Cantabria", "la Comunitat Valenciana", "Extremadura", "Navarra", "Murcia", "La Rioja", "Castilla-La Mancha"},
			"univ":      {"la Universidad de Zaragoza", "la Universidad de Salamanca", "el CSIC", "la Universidad de Granada", "la Universidad de Oviedo", "la Universidad de Navarra", "el Instituto de Salud Carlos III", "la Universidad de Valencia"},
			"hallazgo":  {"dormir menos de seis horas aumenta el riesgo de sedentarismo", "los jóvenes leen más en formato digital que hace diez años", "caminar treinta minutos al día mejora el estado de ánimo", "el uso del transporte público crece entre los universitarios", "las olas de calor reducen el rendimiento escolar", "la dieta mediterránea se abandona en la adolescencia", "el teletrabajo híbrido mejora la conciliación", "los bosques urbanos bajan la temperatura de los barrios", "el tiempo de lectura de los adolescentes ha crecido en tres años", "las ciudades con más árboles registran menos golpes de calor", "la bicicleta compartida reduce los atascos en zonas universitarias", "los mercados de abastos mantienen a los barrios más vivos", "el sueño de los niños mejora con rutinas sin pantallas", "trabajar desde casa dos días reduce el estrés de los empleados"},
			"ciudad":    {"Valladolid", "Zaragoza", "Málaga", "Bilbao", "Alicante", "Vigo", "Gijón", "Córdoba", "Pamplona", "Santander", "Almería", "Badajoz", "Burgos", "Albacete"},
			"obra":      {"un carril bici de doce kilómetros", "una biblioteca con espacio de coworking", "un nuevo sistema de autobuses eléctricos", "un parque inundable en la ribera", "una zona de bajas emisiones", "un mercado gastronómico en el casco antiguo", "un centro de salud con urgencias 24 horas", "una línea nocturna de metro", "un centro cultural en la antigua estación", "un tranvía que unirá tres barrios", "un huerto urbano con aulas al aire libre", "una pasarela sobre el río"},
			"delito":    {"estafas por internet", "robos en comercios", "falsificar entradas de conciertos", "vender alquileres falsos", "hurtos en cajeros", "suplantar a una entidad bancaria"},
			"org":       {"Sanidad", "La DGT", "Protección Civil", "La AEMET", "Los pediatras", "La OCU", "Cruz Roja", "Los veterinarios", "La Policía Nacional", "Los farmacéuticos"},
			"alerta":    {"las estafas con mensajes de falsa entrega", "los riesgos de la conducción con niebla", "el aumento de las intoxicaciones por setas", "la proliferación de mosquitos fuera de temporada", "el uso de pantallas antes de dormir", "los atascos en las salidas de los puentes", "la falta de médicos en el medio rural", "el aumento de las reclamaciones por vuelos cancelados", "el crecimiento de las estafas con códigos QR", "la falta de pediatras en los centros de salud", "el consumo de energía en los hogares este invierno", "la escasez de plazas de aparcamiento en los hospitales"},
			"amp":       {"las becas de comedor", "las plazas de Formación Profesional", "las aulas de dos años", "las ayudas al transporte escolar", "los programas de refuerzo"},
			"tema":      {"el cambio de hora", "la subida de los alquileres de temporada", "la bajada de la natalidad", "el auge de las bicicletas eléctricas", "el cierre de oficinas bancarias", "la sequía invernal", "la brecha digital", "el turismo de cruceros"},
			"colectivo": {"los jóvenes", "los autónomos", "las familias monoparentales", "los pensionistas", "los estudiantes", "los pequeños comerciantes", "los trabajadores del campo"},
		},
		sums: []string{
			"Los datos proceden de la última encuesta publicada por los organismos oficiales.",
			"Los responsables aseguran que el servicio estará disponible antes de que termine el trimestre.",
			"Los vecinos reclaman desde hace años una solución para esta situación.",
			"Los expertos piden prudencia al interpretar las cifras de un solo mes.",
			"La iniciativa cuenta con financiación municipal y con fondos autonómicos.",
		},
	},
	"internacional": {
		patterns: []string{
			"{pais} {pv} {pobj}",
			"{pais} {pv} {pobj}, según {fuente}",
			"La Unión Europea {uev} {ueo}",
			"Cumbre en {sede}: {cum}",
			"{pais}: {inc}",
		},
		fills: map[string][]string{
			"pais":   {"Francia", "Alemania", "Italia", "Portugal", "Marruecos", "Brasil", "Argentina", "México", "Japón", "India", "Reino Unido", "Polonia", "Turquía", "Canadá", "Australia", "Chile", "Países Bajos", "Suecia"},
			"pv":     {"aprueba", "debate", "pospone", "negocia", "presenta", "anuncia", "prepara"},
			"pobj":   {"una reforma del sistema de pensiones", "un nuevo paquete de ayudas a las familias", "su plan de transición energética", "una ley de protección de datos", "un acuerdo comercial con sus vecinos", "una estrategia para atraer talento", "un plan de renovación de ferrocarriles", "una reforma educativa", "medidas contra la inflación", "su presupuesto de defensa", "una ley de movilidad urbana", "un plan para reducir las listas de espera", "una estrategia de vivienda asequible", "la regulación de los alquileres de temporada", "un programa de becas para estudiantes extranjeros", "un impuesto a las bebidas azucaradas"},
			"fuente": {"la prensa local", "fuentes oficiales", "un informe del ministerio", "datos de la oficina estadística"},
			"uev":    {"debate", "aplaza", "aprueba", "estudia", "negocia"},
			"ueo":    {"un nuevo marco para la inteligencia artificial", "la regulación de las baterías para vehículos eléctricos", "un fondo para la innovación industrial", "las normas de etiquetado de alimentos", "el calendario del pacto de migración", "una estrategia de materias primas críticas", "sus objetivos de reducción de emisiones", "el reglamento de seguridad de los juguetes", "la reforma de las normas fiscales", "un programa de intercambio para aprendices", "los límites a los envases de plástico", "un nuevo sistema de pagos instantáneos", "el etiquetado de los productos de pesca", "un plan de conectividad rural"},
			"sede":   {"Ginebra", "Lisboa", "Bruselas", "Nairobi", "Singapur", "Montevideo", "Viena", "Helsinki"},
			"cum":    {"los líderes buscan un acuerdo sobre el comercio de cereales", "se debate la financiación de la adaptación climática", "los ministros de Energía revisan los contratos de gas", "arranca una reunión sobre ciberseguridad global", "se negocia el reparto de cuotas pesqueras", "se analizan los precios de los fertilizantes", "los líderes estudian una moratoria en la pesca de arrastre", "se negocia un código de conducta para la inteligencia artificial", "arranca el foro de inversión en infraestructuras", "los ministros de Sanidad debaten una estrategia contra las pandemias"},
			"inc":    {"una nueva ley regula el teletrabajo en las empresas públicas", "el Parlamento aprueba el presupuesto del próximo año tras un largo debate", "un apagón informático afecta a varias oficinas del Estado", "los museos nacionales amplían su horario durante las fiestas", "el Gobierno presenta un plan de renovación del parque de viviendas públicas", "un puente peatonal inaugurado en la capital se convierte en símbolo de la ciudad", "la cumbre de alcaldes pide más fondos para el transporte", "se celebra el congreso mundial de panaderos", "una exposición recupera el archivo fotográfico de la región"},
		},
		sums: []string{
			"Las autoridades locales han convocado una rueda de prensa para detallar las medidas.",
			"El anuncio llega en un momento de debate sobre el papel de las instituciones europeas.",
			"Los analistas internacionales ven pocas posibilidades de un cambio de rumbo a corto plazo.",
			"La noticia fue adelantada por la prensa local y confirmada después por fuentes oficiales.",
		},
	},
	"deportes": {
		patterns: []string{
			"{eq} vence al {eq2} ({sw}) y {cons}",
			"{eq} empata con el {eq2} ({sd}) en {est}",
			"{eq} cae ante el {eq2} y {cons2}",
			"{jug} {les}",
			"{eq} ficha a {jug} por {n} millones",
			"{ent} {entd}",
			"{dep}: {depd}",
			"Crónica | {eq} - {eq2}: {cron}",
		},
		fills: map[string][]string{
			"eq":    {"CF Sierra Nevada", "Real Valdemar", "Deportivo Alcázar", "UD Sotillo", "Racing de Pinares", "Sporting Vegamar", "CD Montealto", "Atlético Cantábrico", "Real Unión Levante", "Club Ribera del Tajo", "Gimnástica Aldea", "CD Albarracín", "Real Sociedad Valdivia", "Peña Deportiva Cerezo", "CD Marisma", "Atlético Pedroñeras", "UD Los Pinos", "CF Olivares", "Racing Villafranca", "Deportivo Cuenca Alta", "CD Brañosera", "SD Nava del Rey", "CF Montearagón", "Unión Atlética Ebro"},
			"eq2":   {"Racing de Pinares", "Real Valdemar", "CD Montealto", "Sporting Vegamar", "UD Sotillo", "CF Sierra Nevada", "Atlético Cantábrico", "Deportivo Alcázar", "Club Ribera del Tajo", "Gimnástica Aldea", "Real Unión Levante", "Real Sociedad Valdivia", "Peña Deportiva Cerezo", "CD Marisma", "Atlético Pedroñeras", "UD Los Pinos", "CF Olivares", "Racing Villafranca", "Deportivo Cuenca Alta", "CD Brañosera", "SD Nava del Rey", "CF Montearagón", "Unión Atlética Ebro"},
			"sw":    {"2-0", "3-1", "1-0", "2-1", "3-2", "4-1"},
			"sd":    {"1-1", "0-0", "2-2"},
			"cons":  {"se acerca a los puestos europeos", "escala hasta la tercera plaza", "respira en la tabla", "enlaza su tercera victoria seguida", "mantiene el liderato"},
			"cons2": {"se complica la permanencia", "pierde fuelle en la lucha por el título", "suma su cuarta derrota fuera de casa", "cede terreno en la clasificación"},
			"est":   {"un duelo sin goles en la segunda parte", "un partido de ida y vuelta", "un final de infarto", "una noche de lluvia"},
			"jug":   {"Iker Valdés", "Marcos Bellido", "Álvaro Quirós", "Hugo Ferrán", "Nico Salvatierra", "Adrián Olmedo", "Sergio Lasheras", "Dani Robles", "Pablo Ontiveros", "Rubén Cebrián"},
			"les":   {"se pierde el próximo partido por una lesión muscular", "vuelve a entrenar con el grupo tras su lesión", "renueva hasta 2029", "acaricia la convocatoria tras recuperarse", "será duda para el fin de semana"},
			"ent":   {"El entrenador del Real Valdemar", "El técnico del CF Sierra Nevada", "El preparador del Sporting Vegamar", "El míster del Deportivo Alcázar", "El técnico del Racing de Pinares"},
			"entd":  {"defiende a sus jugadores tras la derrota", "pide paciencia al aficionado", "descarta fichajes en el mercado de invierno", "avisa del cansancio acumulado", "elogia la intensidad de su equipo"},
			"dep":   {"Baloncesto", "Tenis", "Ciclismo", "Balonmano", "Fórmula 1", "Atletismo", "Motociclismo"},
			"depd":  {"la jornada deja un nuevo líder en la clasificación", "los favoritos se miden este fin de semana", "gran ambiente en el pabellón", "se aplaza la prueba por el mal tiempo", "la federación confirma el calendario del próximo año"},
			"cron":  {"un derbi para el recuerdo", "una victoria sufrida", "los tres puntos se quedan en casa", "un gol en el descuento lo cambia todo", "un empate con sabor amargo"},
		},
		sums: []string{
			"El técnico reconoció tras el encuentro que el equipo tiene margen de mejora.",
			"La próxima jornada será clave para definir las posiciones de la tabla.",
			"El club informará en las próximas horas del alcance de la situación.",
		},
	},
	"cultura": {
		patterns: []string{
			"{peli} lidera la taquilla en su segundo fin de semana",
			"{libro} gana el premio {premio}",
			"Estreno de {obra} en el teatro {teatro}",
			"{emp_t} presenta {gad}",
			"Los científicos {cie}",
			"{fest} anuncia su cartel con {n} artistas confirmados",
		},
		fills: map[string][]string{
			"peli":   {"«La casa del faro»", "«Ceniza y miel»", "«El último tren a Medina»", "«Cuaderno de abril»", "«Los hijos del río»", "«Tierra de nadie»"},
			"libro":  {"«Los días de sal»", "«Un verano en Calatrava»", "«La memoria del hierro»", "«Cartas desde el Moncayo»", "«El invierno de las abejas»"},
			"premio": {"Ciudad de Santander", "Nacional de Narrativa", "Villa de Valdivia", "Miguel de Aranda", "Letras del Duero"},
			"obra":   {"«Medea en el mercado»", "«La sala de espera»", "«Cena de Navidad»", "«El jardín de los cerezos»", "«Quién teme a la lluvia»"},
			"teatro": {"Español", "de la Abadía", "Principal", "Arriaga", "Lope de Vega", "Calderón"},
			"emp_t":  {"Nordik Devices", "Alcor Labs", "Tecnoval", "Pixelmar", "Cobalto Software"},
			"gad":    {"un móvil plegable con batería de cinco días", "unas gafas de realidad aumentada para el trabajo", "un asistente de voz que funciona sin conexión", "un portátil modular y reparable", "un nuevo modelo de lenguaje para pequeñas empresas"},
			"cie":    {"hallan un nuevo método para reciclar baterías", "descubren una especie de coral en aguas del Cantábrico", "logran medir con precisión la erosión de las playas", "desarrollan un sensor barato para detectar fugas de agua", "publican el mapa más detallado de los glaciares pirenaicos"},
			"fest":   {"El Festival del Duero", "El Aullido Fest", "Mar de Músicas", "El festival Sierra Sonora", "Noches de Ribera"},
		},
		sums: []string{
			"La propuesta llega después de varios meses de preparación y con una acogida desigual entre la crítica.",
			"Los organizadores esperan superar la asistencia de la edición anterior.",
		},
	},
	"bulos": {
		patterns: []string{
			"No, {bulo}",
			"Es falso que {bulo2}: el mensaje que circula por redes es un bulo",
			"Lo que sabemos de {tema_b}: qué es cierto y qué no",
		},
		fills: map[string][]string{
			"bulo":   {"el agua del grifo de Valladolid no está contaminada: es un bulo que circula por WhatsApp", "Correos no cobrará una tasa por recoger paquetes con un enlace de pago", "los supermercados no van a retirar el aceite de oliva por una alerta sanitaria", "no se prohíben las estufas de leña en toda España a partir de enero", "la DGT no multará por llevar chanclas al volante con este nuevo criterio", "el banco no te va a pedir tus claves por SMS para evitar un bloqueo"},
			"bulo2":  {"los hospitales vayan a cobrar por las urgencias", "se retire el carné a quienes superen los 70 años", "un tren turístico haya descarrilado en la sierra", "una cadena de gasolineras regale combustible por su aniversario", "haya una alerta de secuestros en los colegios de la ciudad"},
			"tema_b": {"el nuevo sistema de multas por radares", "los mensajes de falsas entregas de paquetes", "el supuesto cierre de cajeros en pueblos pequeños", "las cadenas de WhatsApp sobre la gripe aviar", "los mensajes que prometen ayudas inexistentes"},
		},
		sums: []string{
			"Hemos contrastado la información con fuentes oficiales y no hay constancia de que sea cierta.",
			"El mensaje se viralizó en cadenas de mensajería durante las últimas horas.",
		},
	},
}

// sectionBucket maps a feed section (as in sources.yaml) to a bucket name.
// "portada" is resolved by the generator with a weighted choice.
func sectionBucket(section string) string {
	switch strings.ToLower(section) {
	case "españa", "nacional", "política":
		return "politica"
	case "economía":
		return "economia"
	case "internacional", "mundo":
		return "internacional"
	case "deportes", "fútbol":
		return "deportes"
	case "sociedad":
		return "sociedad"
	case "bulos", "verificación":
		return "bulos"
	}
	return ""
}

var authors = []string{"Marta Quintana", "Daniel Ibarra", "Lucía Fontán", "Javier Solís", "Elena Marcos", "Rubén Cortés", "Paula Zamora", "Íñigo Sagasta", "Carmen Beltrán", "Andrés Palacios", "Noelia Rivas", "Sergio Montoro", "Irene Valcárcel", "Pablo Arjona", "Teresa Lagos", "Ricardo Olmos", "Beatriz Lanza", "Hugo Castañeda"}
