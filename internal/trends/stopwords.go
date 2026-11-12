package trends

import "strings"

// Words are listed already folded (lowercase, accents stripped, ñ kept).
// They are matched against both the folded token and its stemmed key.

// baseStop: Spanish function words and very frequent, low-information
// verbs/adverbs found in headlines.
const baseStop = `
a al algo alguien alguna algunas alguno algunos algun alla alli ambos ante antes aquel aquella aquellas aquello aquellos
aqui asi aun aunque bajo bastante bien cada casi como con contra cual cuales cuando cuanto cuantos cuanta cuantas de del
dentro desde despues donde dos durante e el ella ellas ello ellos en encima entonces entre era eran eras eres es esa esas
ese eso esos esta estaba estaban estado estamos estan estar estas este esto estos estoy fue fueron fui fuimos ha haber habia
habian hace hacen hacer hacia han hasta hay haya he hecho hemos hoy la las le les lo los mas me mi mia mias mientras mio mios
mis misma mismas mismo mismos mucha muchas mucho muchos muy nada ni ningun ninguna ninguno ningunos ninguna no nos nosotros
nuestra nuestras nuestro nuestros nunca o os otra otras otro otros para pero poco pocos podria podrian poder por porque
pues que quien quienes se sea sean segun ser sera seran seria si sido siempre sin sino sobre sois solo son soy su sus tal
tambien tampoco tan tanto tanta tantos tantas te tendra tendran tener tenia tenian tiene tienen toda todas todavia todo todos
tras tu tus tuvo u un una unas uno unos usted ustedes va vamos van varias varios vas veces vez voy vosotros vuestra vuestro
y ya yo
dice dicen dijo dijeron decir diga digo afirma afirman asegura aseguran anuncia anuncian explica explican senala senalan
indica indican recuerda pide piden pedir quiere quieren puede pueden podra podran deja dejan dejo debe deben debera
va vuelve vuelven vuelta llega llegan pasa pasan pone ponen sale salen tiene tienen hace hacen haria sera seria
ademas sobre entre hacia mediante tras ante bajo cabe contra segun
primer primera primeros primeras segundo segunda tercer tercera ultimo ultima ultimos ultimas nuevo nueva nuevos nuevas
gran grande grandes mayor mayores menor mejor mejores peor otro mismo propio propia propios propias
nuevamente ahora hoy ayer manana anoche tarde temprano luego pronto dia dias semana semanas mes meses año años hora horas
minuto minutos momento fin tiempo parte partes caso casos forma lugar cosa cosas
euros euro millones millon mil cientos
quizas acaso vez veces mas menos tanto igual junto junta junto
uno dos tres cuatro cinco seis siete ocho nueve diez once doce
cualquier cualquiera demas
`

// noiseStop: outlet names, section labels and editorial boilerplate that
// say nothing about the news itself.
const noiseStop = `
elpais elmundo abc lavanguardia eldiario elconfidencial okdiario larazon publico rtve europapress efe epe
20minutos cope cadena ser sexta telecinco antena3 vozpopuli elespanol libertaddigital eldebate theobjective infolibre ctxt
elperiodico lavozdegalicia huffpost agencias agencia redaccion colaboracion press diario periodico
ultima directo vivo video videos vídeo fotos foto fotogaleria galeria imagenes opinion analisis editorial entrevista cronica
claves tribuna carta cartas podcast newsletter suscribete suscripcion leer lee ver vea resumen noticias noticia
actualidad informacion informativo informativos portada hilo minuto directos en-directo
exclusiva exclusivo especial reportaje reportajes premium breaking
tweet tweets twitter instagram facebook whatsapp telegram youtube tiktok
comentarios comentario compartir
`

var months = `enero febrero marzo abril mayo junio julio agosto septiembre setiembre octubre noviembre diciembre`
var weekdays = `lunes martes miercoles jueves viernes sabado domingo`

// connectors may appear inside entity phrases ("Ley de Vivienda").
var connectors = map[string]bool{"de": true, "del": true, "la": true, "las": true, "los": true, "el": true}

func buildStopSet(extra []string) map[string]struct{} {
	m := make(map[string]struct{}, 800)
	for _, blob := range []string{baseStop, noiseStop, months, weekdays} {
		for _, w := range strings.Fields(blob) {
			m[foldString(w)] = struct{}{}
		}
	}
	for _, w := range extra {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		m[foldString(w)] = struct{}{}
	}
	return m
}
