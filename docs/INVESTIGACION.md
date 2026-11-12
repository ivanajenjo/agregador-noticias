# Investigación: productos comparables, métodos y hoja de ruta

Fecha de la investigación: 2026-11-12. Documento de trabajo, sin código.

**Convención de fiabilidad.** Las afirmaciones con enlace proceden de fuentes que se localizaron en esta investigación. Las marcadas **[sin verificar]** son conocimiento general del autor o proceden de fuentes secundarias de baja calidad: hay que contrastarlas antes de apoyarse en ellas. Los enlaces a artículos jurídicos y a paneles de datos se localizaron por búsqueda y no se abrieron en su totalidad.

## Resumen ejecutivo

1. Nuestro nicho (comparar el encuadre de medios españoles, autoalojado, sin LLM obligatorio, en una Pi) no lo cubre ningún producto localizado. Ground News y AllSides son SaaS en inglés centrados en EE. UU.; Media Cloud y GDELT son infraestructuras de investigación; FreshRSS y Miniflux son lectores, no analizadores.
2. El diferencial más defendible es lo que otros no hacen: capturas históricas de portada, seguimiento de cambios de titular, vocabulario diferencial por línea editorial y "puntos ciegos" calculados con etiquetas editables por el usuario.
3. Las etiquetas de sesgo de terceros (AllSides, MBFC, Ground News) mezclan encuestas, revisión editorial y datos de terceros. Su propia documentación admite que son instantáneas limitadas. Mantener las nuestras editables y mostrarlas como subjetivas es la decisión correcta.
4. Para vocabulario diferencial, el método de referencia es el log-odds con prior de Dirichlet informativo (Monroe, Colaresi y Quinn, 2008). Es una cuenta de contadores: cabe en la Pi sin LLM. Dunning/Rayson-Garside (G²) es la alternativa clásica de keyness.
5. Para ráfagas, Kleinberg (2002/2003) es la referencia formal. Para nuestro volumen basta una razón de log-verosimilitud contra línea base con mínimos de medios distintos, que ya está en RF7.
6. Para historias, el patrón publicado es clustering online por idioma y enlace entre clusters (Event Registry; newsLens). Con ≤ 30 k artículos, el solapamiento de entidades y términos más embeddings ya calculados da buen resultado.
7. Los LLM pequeños son aceptables anotando, pero con riesgos documentados (sensibilidad a pistas partidistas, variabilidad entre ejecuciones). Hay que validar con un conjunto manual pequeño y guardar siempre modelo y versión de prompt.
8. Legal: el límite del art. 32.2 del TRLPI y el nuevo derecho conexo de editores (art. 129 bis, RDL 24/2021) afectan a agregadores que publican fragmentos. Para uso personal en LAN sin republicar el riesgo es bajo, pero conviene no exponer el servicio públicamente.
9. Las portadas se pueden comparar con hash perceptual (dHash/pHash, Hamming) en Go puro; hay bibliotecas disponibles (`goimagehash`).
10. Cinco extras recomendados (sección 5): conjunto de oro y panel de evaluación, ficha de medio, veto/fusión manual de historias, consultas guardadas como feeds Atom, y posición de los titulares en portada.

---

## 1. Productos y proyectos comparables

### 1.1 Ficha por producto

| Producto | Qué hace | Qué valoran los usuarios | Qué aprender | Qué evitar |
|---|---|---|---|---|
| **Ground News** | Agrega una noticia desde muchas fuentes y muestra una barra de distribución por sesgo; informe semanal "Blindspot" con historias cubiertas casi solo por un lado. Toma las etiquetas de AllSides, Media Bias/Fact Check y Ad Fontes ([help.ground.news](https://help.ground.news/en/categories/2272705), [about.ground.news/blindspot](https://about.ground.news/blindspot), [rating system](https://ground.news/rating-system)). | Ver de un vistazo quién cubre qué y qué lado no lo cubre. | La métrica "porcentaje de cobertura por lado" y el concepto de blindspot son el modelo de nuestra sección de omisiones. | Presentar como objetiva una etiqueta tomada de terceros. Hay blogs que describen un "perfil personal de lectura"; no se confirmó en fuentes oficiales **[sin verificar]**. |
| **AllSides** | Etiqueta fuentes en cinco niveles (Izquierda a Derecha). Combina encuestas ciegas (se oculta la marca), revisión editorial y datos de terceros ([métodos](https://www.allsides.com/media-bias/media-bias-rating-methods)). | Transparencia metodológica; la encuesta ciega es un buen diseño. | Admiten que la encuesta evalúa un fragmento pequeño en el tiempo y que la revisión editorial puede anularla. Replicar la idea: la etiqueta es un juicio, con historial de cambios. | Escala de 5 niveles pensada para EE. UU.: no se traslada limpiamente al eje español, donde pesan también el eje territorial y el europeísmo. |
| **Media Bias/Fact Check** | Calificación por un equipo pequeño y voluntario, con votos de lectores como contraste. Según un resumen secundario antiguo, mínimo de 10 titulares y 5 fuentes **[sin verificar: la página metodológica propia no se localizó]**. | Cobertura amplia y fácil de consultar. | Mostrar de dónde sale cada etiqueta. | Mezclar "sesgo" y "factualidad" en un solo número. |
| **Media Cloud** | Plataforma abierta de Harvard Berkman Klein y MIT Center for Civic Media para estudiar ecosistemas de medios; más de 1,7 mil millones de historias desde 2008; comparte metadatos y datos derivados, no el texto completo, por copyright ([cyber.harvard.edu](https://cyber.harvard.edu/gsoc/Media_Cloud), [ICWSM 2021](https://ojs.aaai.org/index.php/ICWSM/article/view/18127), [arXiv 2104.03702](https://arxiv.org/pdf/2104.03702v2)). | Rigor, colecciones de medios por país, API. | Su criterio de compartir derivados y no texto es una buena pauta legal para nuestra API/exportación. | Escala de infraestructura de investigación; no replicable en una Pi. |
| **GDELT** | Eventos y Grafo de Conocimiento actualizados cada 15 min; traducción de 65 idiomas; extrae personas, organizaciones, lugares, temas y "emociones"; accesible vía BigQuery ([blog GDELT](https://blog.gdeltproject.org/gdelt-2-0-our-global-world-in-realtime/), [Translingual](https://blog.gdeltproject.org/?p=1369)). | Amplitud global y gratuidad. | Idea de normalizar entidades y temas por artículo. | Ruido y precisión variable de la extracción automática a escala; no hay segmentación por línea editorial española. |
| **Event Registry** | Agrupa artículos en eventos con clustering online por idioma y enlaza clusters entre idiomas (Leban et al., WWW 2014; Rupnik et al., JAIR 2016) ([JAIR](https://jair.org/index.php/jair/article/view/10981), [arXiv 1512.07046](https://arxiv.org/html/1512.07046), [WWW 2014](https://archives.iw3c2.org/www2014/proceedings/companion/p107.pdf)). | Eventos como unidad de análisis. | Es el patrón de nuestras "historias": clustering online en ventana corta. | Servicio comercial; no consta su estado actual en las fuentes halladas. |
| **NewsWhip** | Monitoriza interacción social de artículos y predice la que alcanzarán hasta 24 h después ([newswhip.com/prediction](https://www.newswhip.com/prediction)). | Aviso temprano de lo que "va a explotar". | Valor de un "índice de velocidad" de cobertura. | Depende de datos de redes sociales que no tenemos; marketing sin evaluaciones independientes halladas. |
| **Google News "Full Coverage"** | Resumen de una historia con múltiples fuentes, desde 2018, llevado también a Search ([Search Engine Land](https://searchengineland.com/google-brings-full-coverage-news-to-search-results-346727)). | Contexto de una historia en una sola pantalla. | Es nuestra página de detalle de historia. | No se localizó documentación de Google sobre cómo elige fuentes. |
| **NewsDiffs y similares** | Compara versiones de un artículo y marca añadidos y borrados; creado en 2012 por Jennifer 8. Lee, Eric Price y Greg Price; código abierto ([Wikipedia](https://en.wikipedia.org/wiki/NewsDiffs), [Poynter](https://www.poynter.org/news/newsdiffs-tracks-changes-new-york-times-cnn)). | Descubrir ediciones sigilosas. | Las reseñas señalaron la dificultad de encontrar cambios sustanciales entre un flujo exhaustivo: hay que **puntuar la importancia del cambio**. | Mostrar todos los cambios triviales (puntuación, tildes). |
| **Wayback / archivos de portadas** | Archivo web general. | Referencia histórica. | Nuestras capturas son un archivo propio y reproducible de las portadas con la hora exacta. | No se localizó en esta búsqueda información sobre las colecciones de portadas del Internet Archive **[sin verificar]**. |
| **Newtral y Maldita.es** | Verificadores españoles firmantes del código de la IFCN, junto con EFE Verifica y Verificat ([Maldita](https://maldita.es/maldita-es-firmante-del-codigo-de-principios-del-international-fact-checking-network/), [Newtral](https://newtral.es/quienes-somos/)). | Contexto de rumores y bulos. | Posible enlace futuro: mostrar si una historia aparece en una verificación (enlace manual o por búsqueda, no automático). | Mezclar "verificado" con "línea editorial". La fecha de fundación de Newtral difiere entre fuentes (2017 vs 2018): no citarla. |
| **FreshRSS / Miniflux** | Lectores RSS autoalojados. FreshRSS tiene extensiones, entre ellas una de webhook por palabras clave; Miniflux integra ntfy, Gotify y webhooks genéricos ([FreshRSS Extensions](https://github.com/FreshRSS/Extensions), [Miniflux](https://miniflux.app/docs)). Las referencias de ntfy en Miniflux proceden de comparativas y forks: verificar en la documentación oficial. | Ligereza, filtros, patrones de notificación simples. | Patrón de alertas: una regla por palabra clave y un webhook; ntfy con prioridad y topic ([docs ntfy](https://docs.ntfy.sh/publish/), no abierta en esta investigación). | Convertirnos en un lector de propósito general. |

### 1.2 Matriz de funcionalidades

Leyenda: ✔ sí, ◐ parcial, ✘ no, 🕓 previsto en nuestra hoja de ruta. Valoraciones basadas en las descripciones públicas anteriores; donde no se pudo confirmar, está en blanco.

| Funcionalidad | Nuestro producto | Ground News | AllSides | Media Cloud | GDELT | Event Registry | NewsDiffs | FreshRSS/Miniflux |
|---|---|---|---|---|---|---|---|---|
| Autoalojado | ✔ | ✘ | ✘ | ✔ (código abierto) | ✘ | ✘ | ✔ | ✔ |
| Medios españoles por línea editorial | ✔ | ◐ | ✘ | ◐ | ◐ | ◐ | ✘ | ✘ |
| Etiquetas editables por el usuario | ✔ | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ |
| Historias / clustering | ✔ | ✔ | ✘ | ◐ | ✔ | ✔ | ✘ | ✘ |
| Puntos ciegos (omisión) | 🕓 (ya hay lista de medios que no cubren) | ✔ | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ |
| Capturas de portada | ✔ | ✘ | ✘ | ✘ | ◐ (Frontpage Graph, según una guía secundaria) | ✘ | ✘ | ✘ |
| Cambios de titular | 🕓 | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✘ |
| Búsqueda híbrida + RAG | ✔ | ✘ | ✘ | ✘ | ✘ | ◐ | ✘ | ✘ |
| Vocabulario diferencial | 🕓 | ✘ | ✘ | ◐ (nubes de palabras) | ✘ | ✘ | ✘ | ✘ |
| Alertas | 🕓 | ✔ | ✘ | ✘ | ✘ | ✔ | ✘ | ✔ |
| Funciona sin LLM | ✔ | n/a | n/a | n/a | n/a | n/a | ✔ | ✔ |

**Conclusión.** Ningún producto combina portadas históricas, titulares cambiantes, vocabulario diferencial y etiquetas editables de forma local. Esa combinación es la propuesta de valor.

---

## 2. Métodos para las funcionalidades planificadas

Convención para la Pi: todo lo que sea contadores, hashes y SQLite se calcula localmente. Lo que requiera un modelo se delega al servidor remoto y se tolera que esté apagado (principio ya fijado en `docs/CONTRIBUIR.md`).

### 2.a Detección de ráfagas y tendencias

| Técnica | Idea | Coste | Notas de implementación |
|---|---|---|---|
| **Kleinberg (KDD 2002; DMKD 2003)** | Autómata de estados infinitos; ráfagas como transiciones, con estructura jerárquica. Mejor que umbrales fijos en flujos ruidosos ([resumen](https://datalearner.com/academic/journal-papers/1384-5810/volumes-and-issues/7/paper-detail/9118), [KDD 2002](https://bibtex.github.io/KDD-2002-Kleinberg.html)). | Bajo (programación dinámica por término). | Aplicar solo a los N términos candidatos de la ventana, no a todo el vocabulario. Existe una implementación de referencia en R (`bursts`). |
| **Razón de log-verosimilitud G² (Dunning 1993; Rayson y Garside 2000)** | Contrasta la frecuencia de un término en ventana reciente frente a la línea base ([Rayson y Garside](https://aclanthology.org/W00-0901/)). | Muy bajo. | Es la opción práctica para RF7. Añadir suavizado y exigir ≥ k medios distintos. |
| **TF-IDF frente a línea base** | Puntúa por frecuencia relativa. | Muy bajo. | Sobrevalora términos raros: aplicar mínimo de documentos. |
| **Tendencias tipo "Twitter"** | Comparar velocidad actual con la histórica del término **[sin verificar: no se localizó fuente primaria]**. | Bajo. | Equivale a G² con ventana deslizante. |

**Recomendación.** Mantener G² + suavizado + mínimo de medios (ya en RF7). Kleinberg solo como mejora para etiquetar la duración de una ráfaga. Guardar el valor de la puntuación para poder auditar por qué un término salió como tendencia.

### 2.b Historias / clustering

- Patrón publicado: clustering online dentro de cada idioma y enlace posterior entre clusters (Event Registry: [JAIR](https://jair.org/index.php/jair/article/view/10981)). newsLens construye primero clusters locales y los fusiona en historias largas, de forma online ([ACL W17-2701](https://aclanthology.org/W17-2701/)).
- Para nosotros, un solo idioma y ventana corta: solapamiento de entidades y bigramas ponderados (ya implementado) más similitud de embeddings cuando existan. Union-find o clustering greedy contra centroides de las últimas 48 h es suficiente con ≤ 30 k artículos.
- Mejoras posibles: (1) fusión de clusters que comparten ≥ 2 entidades raras; (2) **partir** clusters que unen dos eventos distintos (el error más visible para el usuario); (3) botón "no es la misma historia" que guarde un veto en BD y sirva de evaluación.
- Riesgo: cadenas de similitud que pegan temas amplios ("Gobierno", "Cataluña"). Mitigación: tamaño máximo de cluster y penalización de términos muy frecuentes.

### 2.c Encuadre y vocabulario diferencial

- **Fightin' Words** (Monroe, Colaresi y Quinn, *Political Analysis* 16(4), 2008): log-odds con prior de Dirichlet informativo; la frecuencia en un corpus general actúa de prior y contrae hacia cero las estimaciones de palabras raras; resultado en z-score ([Cambridge](https://www.cambridge.org/core/journals/political-analysis/article/fightin-words-lexical-feature-selection-and-evaluation-for-identifying-the-content-of-political-conflict/81B3703230D21620B81EB6E2266C7A66), [IDEAS](https://ideas.repec.org/a/cup/polals/v16y2008i04p372-403.html), [transparencias JHU](https://nlp-css-601-672.cs.jhu.edu/sp2026/files/0126.LogOdds.pdf)). Hay una implementación pública en Python (gist de Thiago Marzagao) que sirve de referencia para portar a Go.
- **Keyness por G²**: más sencillo y comparable con lo que ya calculamos para tendencias; sobrevalora términos frecuentes en corpus grandes.
- Notas de implementación:
  - Prior = frecuencias de todo el corpus de los últimos 30–60 días (no de otro idioma).
  - Dos grupos: línea editorial A frente a el resto, o medio frente a el resto. Con grupos desiguales el z-score sigue siendo comparable.
  - Mínimo de ~20 apariciones y filtro del vocabulario ruido (nombres de medios, secciones).
  - Para comparar encuadre de **una misma historia**, calcular dentro de las historias (más pequeño: usar prior más fuerte).
- **Cautelas.** Las diferencias léxicas reflejan también temas, secciones y estilo editorial, no solo ideología. La UI debe llamarlas "vocabulario que distingue", no "sesgo". Con líneas editoriales editables por el usuario, los resultados dependen de ellas.

### 2.d Puntos ciegos / omisiones

- Definición de trabajo, tomada del concepto de Ground News (historias cubiertas desproporcionadamente por un lado): para cada historia con ≥ N medios, calcular el reparto por línea editorial y marcar como punto ciego aquellas donde un grupo cubre < X % de lo esperado según su tamaño.
- Hay que normalizar por **número de medios por grupo** (si hay 8 medios de un lado y 3 del otro, 8 a 3 no es sesgo). Comparar contra la tasa base del grupo en la ventana.
- Distinguir tres casos: no publicó, publicó tarde (retardo) y publicó solo como breve. Un medio sin RSS válido no puede contarse como omisión (los feeds rotos deben quedar excluidos y avisados: ya se documentan en `config/sources.yaml`).
- Riesgo principal: **falsos puntos ciegos** por feeds incompletos o por mala agrupación de historias. Mostrar siempre la cobertura de datos ("N de M medios con feed activo").

### 2.e Cambios de titular (ediciones sigilosas)

- Precedente: NewsDiffs ([Wikipedia](https://en.wikipedia.org/wiki/NewsDiffs)). Su crítica recurrente fue el exceso de cambios triviales: puntuar la importancia.
- Hay evidencia académica de que los titulares cambian tras la publicación con frecuencia: un estudio (NSF PAR) reunió unos 30 930 artículos con al menos un cambio de titular y estimó en torno al 23 % de ediciones percibidas como dañinas ([par.nsf.gov](https://par.nsf.gov/servlets/purl/10326932)); y las redacciones usan pruebas A/B de titulares, hasta una docena de variantes por artículo según entrevistas ([Hagar y Diakopoulos 2019](https://doaj.org/article/2583b124fca44a6f90a75d3195139033)). Importante: un cambio de titular puede ser una prueba A/B y no una corrección; no atribuir intención.
- Implementación: en cada ingesta, comparar titular normalizado con el último almacenado; guardar revisión solo si la distancia (Levenshtein por palabras, o Jaccard de tokens) supera un umbral y no es solo puntuación/tildes/mayúsculas. Puntuar: palabras añadidas o eliminadas, cambio de cifra, cambio de negación, cambio de entidad. Mostrar diff por palabras.
- Límite práctico: solo vemos el titular **del feed** (puede diferir del de la página). Documentarlo. Frecuencia de sondeo cada 30 min limita lo que se detecta; un titular que cambia a los 10 min puede escaparse.

### 2.f Anotación de tono, intención y encuadre con modelos pequeños

- **Zero-shot funciona razonablemente, pero con sesgos.** En un estudio sobre mensajes políticos, ChatGPT-4 superó a expertos y a crowdworkers en exactitud y fiabilidad en zero-shot, con mayor fiabilidad a menor temperatura ([arXiv 2304.06588](https://arxiv.org/pdf/2304.06588)). Pero otra nota de investigación argumenta que los LLM usan las etiquetas partidistas como pistas, igual que codificadores humanos, y que los resultados varían entre modelos y entre ejecuciones del mismo modelo ([arXiv 2408.15895](https://arxiv.org/abs/2408.15895)). Conclusión de los autores: validar siempre con datos de referencia. Modelos pequeños abiertos se evaluaron también en anotación de veracidad política ([arXiv 2411.05775](https://arxiv.org/html/2411.05775v1)); no se extrajeron cifras.
- **Salida estructurada.** Ollama admite restringir la salida a un esquema JSON desde diciembre de 2024 (v0.5.0 o posterior), convirtiendo el esquema en gramática ([Ollama blog](https://registry.ollama.ai/blog/structured-outputs); guía de terceros con Qwen3: [glukhov.org](https://www.glukhov.org/post/2025/09/llm-structured-output-with-ollama-in-python-and-go/)). Aun así hay que parsear y validar en el servicio; usar temperatura 0.
- **Modelos candidatos en español.** No se localizó ninguna comparativa fiable de calidad en español entre Qwen3, Gemma, Llama y Phi a tamaños pequeños. **Recomendación metodológica: construir una evaluación propia** (ver 5.5) con 150–300 titulares etiquetados a mano y comparar 2–3 modelos antes de elegir. Qwen3 aparece como opción mejor documentada para JSON restringido en Ollama; Gemma, en cambio, parece depender más del prompt **[sin verificar]**.
- **Alternativa de clasificador ligero.** Los codificadores en español de la familia MarIA (RoBERTa-bne, BSC, entrenado con los archivos web de la Biblioteca Nacional; [HF base](https://huggingface.co/PlanTL-GOB-ES/roberta-base-bne), [nota BSC](https://bsc.es/news/bsc-news/first-massive-artificial-intelligence-system-the-spanish-language-maria-begins-summarize-and)) son la base natural para afinar un clasificador de tono/intención con unos cientos de ejemplos. Necesita un paso de entrenamiento fuera de la Pi; la inferencia por lotes en CPU es viable pero habría que medirla.
- **Prácticas.** (1) Esquema fijo con etiquetas cerradas, puntuación y "justificación" corta citando una frase del texto. (2) Guardar modelo, versión del prompt y temperatura en `article_annotations`. (3) **Ocultar el nombre del medio y las etiquetas políticas del prompt**: así se evita la sensibilidad a pistas partidistas. (4) Calibración: pedir una etiqueta y una confianza, y medir sobre el conjunto de oro si la confianza predice los aciertos. (5) Evaluar acuerdo entre dos ejecuciones y entre dos modelos; descartar etiquetas inestables. (6) Mostrar siempre como estimación (ya fijado en `docs/CONTRIBUIR.md`).

### 2.g Resumen diario con LLM, con citas

- Patrón: seleccionar historias por fórmula determinista (ya la tenemos), pasar al modelo solo titulares y fragmentos numerados, exigir citas `[n]` y verificar que cada cita existe. Reutilizar el modo RAG existente.
- Defensas contra alucinaciones: (1) la plantilla del resumen estructura la salida (titular, quién cubre, qué difiere), el modelo solo redacta 1–2 frases; (2) comprobación automática: cada frase debe llevar una cita y las cifras y nombres propios deben aparecer en los fragmentos; (3) **plan B sin LLM**: resumen extractivo (titular representativo + lista de medios). Funciona con el Mac apagado.
- Publicación: Atom con un ítem por día, enlaces a artículos originales y fragmentos cortos (ver legal, sección 3).

### 2.h Alertas: fatiga, deduplicación y umbrales

- No se encontró una fuente académica específica en esta búsqueda; lo siguiente es práctica de ingeniería **[sin verificar]**.
- Reglas: (1) una alerta por **historia**, no por artículo; (2) ventana de silencio por regla y por historia (p. ej. 6 h), con reapertura solo si crece el número de medios; (3) agrupar en un resumen cuando salten varias en una hora; (4) umbral mínimo de medios distintos; (5) límite diario por regla; (6) horas de silencio nocturnas con prioridad alta como excepción.
- Canal: ntfy con topic y prioridad (cabecera `X-Priority` de 1 a 5, según guías de terceros; autoalojable y con tokens de acceso; [docs ntfy](https://docs.ntfy.sh/publish/) para verificar). Gotify y webhook genérico como alternativas, igual que Miniflux/FreshRSS.
- Métrica: guardar si el usuario abrió la alerta, para ajustar umbrales.

### 2.i Capturas: archivo y comparación

- **Hash perceptual.** En Go existe `goimagehash` (aHash, dHash, pHash y variantes extendidas; distancia de Hamming) y `ajdnik/imghash` (8 algoritmos; requiere Go reciente) ([pkg.go.dev](https://pkg.go.dev/github.com/ajdnik/imghash), [goimagehash](https://docsearch.algolia.com/mcp/docs/repo/corona10/goimagehash)). Una guía de terceros sugiere que con pHash de 64 bits, 0–10 bits distintos indica la misma imagen y más de ~20 imágenes distintas: ajustar con datos propios **[sin verificar]**.
- Para portadas largas, el hash global es poco sensible. Mejor **hash por bandas** (p. ej. 8 franjas horizontales) y comparar franja a franja para localizar dónde cambió la portada, y un diff de píxeles sobre una miniatura.
- Visualización: deslizador de comparación (dos imágenes superpuestas con `clip-path`), línea de tiempo y *timelapse* (secuencia de JPEG ya almacenados; sin vídeo, para no cargar la Pi).
- Ahorro de disco: no almacenar una captura cuyo hash sea casi idéntico a la anterior más de N veces (guardar solo una referencia).
- Texto de portada: si la captura es un DOM renderizado, extraer los titulares y su **posición** (orden y tamaño) es más informativo que los píxeles: "qué titular abre la portada" es el dato editorial. Requiere el DOM en el momento de la captura.

---

## 3. Contexto específico de España

### 3.1 Panorama de medios y etiquetas políticas

- **Fuente de calidad: Reuters Institute Digital News Report (DNR), edición España**, elaborado con la Universidad de Navarra y encuesta online de YouGov (enero de 2025). Cerca del 70 % de los españoles dice no confiar en la mayoría de las noticias y casi el 40 % las evita con regularidad ([Navarra](https://en.unav.edu/web/digital-news-report/entradas/-/blogs/metodologia-2), [ACN](https://www.catalannews.com/society-science/item/nearly-70-of-spaniards-dont-trust-news-report-finds), [Reuters Institute España 2025](https://reutersinstitute.politics.ox.ac.uk/digital-news-report/2025/spain)). La página de país no pudo leerse completa en esta investigación.
- **Posicionamiento.** El DNR mide audiencias y confianza, no "sesgo" de los medios. Para la posición ideológica percibida se localizó un estudio citado por Numeroteca donde El Mundo (6,2), ABC (7,0) y La Razón (6,8) se perciben a la derecha, y La Vanguardia (4,0) y El Periódico de Cataluña (3,2) más al centro, sin cambios relevantes desde 2008 ([numeroteca.org](https://numeroteca.org/?p=6448)). Es un dato secundario: verificar el estudio original antes de citarlo.
- **Audiencias y polarización.** Majó-Vázquez y González-Bailón, con datos del DNR, concluyen que las audiencias de medios de izquierda se polarizan menos y las de medios más a la derecha se han homogeneizado ([ESADE](https://www.esade.edu/ecpol/es/publicaciones/polarizacion-medios-espana/)). Un análisis de Público con datos del CIS indica que los lectores suelen situarse más a la izquierda que el medio que leen y que los conservadores consumen casi solo medios afines ([Público](https://www.publico.es/sociedad/personas-conservadoras-leen-prensa-les-afin-ideologicamente.html)). Un estudio de 2022 sobre el caso del rey emérito observó alto partidismo en El País, elDiario.es y ABC (resultado de búsqueda, sin enlace verificado: localizar el artículo original antes de citarlo).
- **Cautelas.** (1) Los ejes en España no son solo izquierda-derecha: el territorial (centralismo, independentismo) es igual de relevante y una etiqueta lineal lo aplasta. Sugerencia: permitir **etiquetas múltiples** (ideología, territorio, titularidad: público, privado, cooperativo). (2) Un medio no es homogéneo: opinión y noticia difieren; las etiquetas por medio son una simplificación. (3) Nuestras etiquetas son del usuario y subjetivas: la UI debe decirlo.

### 3.2 Aspectos legales (resumen, no es asesoramiento jurídico)

| Aspecto | Lo hallado | Implicación |
|---|---|---|
| **Límite de agregadores (art. 32.2 TRLPI)** | La Ley 21/2014 introdujo un límite específico para agregación de contenidos con compensación equitativa irrenunciable por gestión colectiva (la llamada "tasa Google"); la CNMC advirtió de barreras de entrada ([Clifford Chance](https://www.cliffordchance.com/content/dam/cliffordchance/briefings/2015/02/reforma-de-la-ley-de-propiedad-intelectual.pdf), [CNMC](https://www.cnmc.es/sites/default/files/editor_contenidos/Notas%20de%20prensa/2014/20140528_NP_Tasa_Google_def.pdf)). | Régimen histórico; ver reforma de 2021. |
| **RDL 24/2021** | Eliminó el "canon AEDE" del art. 32.2 y creó el derecho conexo de editores de prensa en el art. 129 bis TRLPI (según [Cuatrecasas](https://www.cuatrecasas.com/es/spain/propiedad-intelectual/art/transposicion-directiva-derechos-de-autor-ii) y [Garrigues](https://www.garrigues.com/es_ES/noticia/ocho-claves-reforma-ley-propiedad-intelectual-aprobada-gobierno-real-decreto-ley)). Las fuentes discrepan en la numeración de apartados: **verificar el texto consolidado del BOE** (BOE-A-2021-17910, según una fuente). Se ha comentado un caso Google-Cedro en primera instancia sobre fragmentos pequeños ([GA-P](https://ga-p.com/publicaciones/compensacion-equitativa-por-la-insercion-de-pequenos-fragmentos-de-texto-google-gana-en-primera-instancia-a-cedro/)); no se comprobó su estado. | Mostrar titular, medio, fecha, enlace y como mucho un fragmento muy corto; **no republicar el texto completo**. La directiva excluye de su ámbito el uso privado y los hipervínculos **[sin verificar en esta investigación]**. |
| **Minería de textos y datos (art. 67)** | Permite reproducir y extraer obras de acceso lícito para minería de datos; el titular puede reservarse el derecho por medios legibles por máquina; no ampara la comunicación pública del material ajeno ([Garrigues](https://www.garrigues.com/es_ES/garrigues-digital/ia-derechos-autor-legibilidad-maquina-accionabilidad-maquina-opt-out-tdm), [Ecija sobre Hamburgo](https://www.ecija.com/en/news-and-insights/alemania-el-tribunal-superior-de-hamburgo-refuerza-el-modelo-europeo-de-opt-out-tecnico-en-mineria-de-datos/)). Las fuentes discrepan en la numeración (67 / 67-bis / 67-ter). | Nuestra indexación local para análisis es el caso favorable; respetar `robots.txt` y cualquier reserva expresa (cabeceras, términos) como política prudente. No se halló una fuente española que declare `robots.txt` como mecanismo jurídicamente vinculante. |
| **Derecho de cita (art. 32.1)** | Cita con fines de análisis o comentario, con indicación de fuente y autor. | Compatible con RAG que cita fragmentos con enlace, siempre que el fragmento sea el necesario y se atribuya. No consta jurisprudencia específica en las búsquedas. |
| **Datos personales (RGPD)** | No tratamos datos personales del usuario. Los autores firman con su nombre: dato mínimo público. | Riesgo bajo. Si se añaden más usuarios o analítica, revisar. |
| **Capturas de portada** | Reproducen la maquetación y el contenido. | Para uso personal en LAN el riesgo es bajo; **no publicar las capturas a Internet** ni redistribuirlas. |

**Recomendación práctica.** Mantener el servicio en LAN con auth opcional (ya previsto), limitar exportaciones y el Atom del resumen a titular + enlace + fragmento corto, respetar `robots.txt` y rate limits, identificar el User-Agent. Un asesor jurídico debería revisar cualquier uso público.

---

## 4. Evaluación de cobertura de la hoja de ruta ya decidida

| Elemento decidido | Respaldo encontrado | Riesgo clave |
|---|---|---|
| Alertas + vigilancias | Patrón ntfy/webhook en Miniflux y FreshRSS; práctica anti-fatiga | Fatiga: agrupar por historia |
| Vocabulario diferencial + puntos ciegos | Monroe et al. 2008; concepto Ground News | Etiquetas subjetivas; falsos puntos ciegos por feeds incompletos |
| Cambios de titular | NewsDiffs; estudios de ediciones y pruebas A/B | Ruido de cambios triviales; confundir A/B con corrección |
| Resumen diario con Atom | Patrón RAG con citas | Alucinación y límites de cita |
| Comparador / timelapse de capturas | Hash perceptual en Go | Tamaño de disco; derechos de imagen |
| Anotación de tono/intención | Estudios de LLM como anotadores | Sensibilidad a pistas partidistas; calidad en español sin medir |
| Retención / métricas / OpenAPI | Práctica estándar | Ninguno relevante |

---

## 5. Ideas adicionales, ordenadas por valor / esfuerzo / viabilidad en Pi

Escalas: valor (A/M/B), esfuerzo (S/M/L), Pi (✔ sin problema, ◐ con cuidado, ✘ requiere el LLM remoto).

| # | Idea | Valor | Esfuerzo | Pi | Por qué |
|---|---|---|---|---|---|
| 1 | **Conjunto de oro y panel de evaluación** (titulares etiquetados a mano para tono, "misma historia", etiqueta de línea) con métricas de acuerdo | A | S | ✔ | Condición previa para confiar en anotaciones, clustering y comparación de modelos. Las fuentes insisten en validar |
| 2 | **Ficha de medio**: perfil con volumen, hora típica de publicación, retardo medio frente al primero, tasa de cambio de titular, vocabulario distintivo, nº de puntos ciegos | A | M | ✔ | Reutiliza datos que ya habrá; da una vista por medio y no solo por noticia |
| 3 | **Veto y fusión manual de historias** ("no es la misma historia", "unir con...") | A | S | ✔ | Corrige el error más visible del clustering y alimenta el conjunto de oro |
| 4 | **Consultas guardadas con Atom/JSON propio** (cualquier búsqueda o vigilancia expuesta como feed) | M | S | ✔ | Integra con lectores como FreshRSS/Miniflux; se apoya en la infraestructura del resumen |
| 5 | **Posición en portada**: extraer los titulares y su orden/tamaño al capturar, y mostrar quién abre portada con qué | A | M | ◐ | Es el dato editorial más fuerte y nadie lo ofrece en esta forma; requiere guardar el DOM |
| 6 | **Retardo e historia de primera publicación** (línea de tiempo "quién la sacó primero y quién la siguió") | M | S | ✔ | Ya existe "quién la publicó primero"; añadir distribución de retardos por medio |
| 7 | **Etiquetas multieje** (ideología, territorio, titularidad, ámbito) con filtros combinables | M | M | ✔ | Evita aplanar el contexto español en un único eje |
| 8 | **Detección de citas y fuentes de agencia** (EFE, Europa Press): marcar noticias que son copia de agencia | M | M | ✔ | Distingue "10 medios la cubren" de "10 medios reproducen un despacho"; mejora los puntos ciegos |
| 9 | **Enlace a verificaciones** (Maldita, Newtral): buscar manualmente o por palabras clave si una historia tiene verificación y mostrar el enlace | M | M | ◐ | Contexto útil; requiere consultar sus sitios respetando sus términos y *robots.txt* |
| 10 | **Mapa de entidades y co-menciones** (qué personas/partidos se mencionan juntos por línea) | M | M | ✔ | Cálculo local con las entidades ya extraídas |
| 11 | **Preguntas guardadas con re-ejecución semanal** (el RAG comparar con memoria: "cambió el encuadre desde la semana pasada") | M | M | ✘ | Útil, pero depende del LLM remoto |
| 12 | **Modo "lectura lenta" semanal** (un informe de lo que cambió: titulares reescritos, tendencias, puntos ciegos) | M | S | ◐ | Reutiliza piezas ya hechas y reduce la presión de alertas diarias |

### Detalle de las cinco recomendadas

**1. Conjunto de oro y panel de evaluación.**
- Modelo de datos: `gold_labels(id, kind, article_id|story_id, label, annotator, created_at)`; kinds: `tone`, `intent`, `same_story`, `lean`.
- UI: pantalla de etiquetado con atajos de teclado, una tarjeta por titular con el medio oculto; página de métricas (acuerdo, matriz de confusión) por modelo y versión de prompt.
- Riesgos: sesgo del propio etiquetador (el usuario); empezar con 150 ejemplos y doble etiquetado de un 20 % en distintos días para medir consistencia.

**2. Ficha de medio.**
- Datos: consultas agregadas por `source_id`; sin tablas nuevas salvo caché.
- UI: página `/medios/{id}` con sparkline de volumen, retardo mediano frente al primero, tasa de revisiones de titular, términos distintivos, historias omitidas.
- Riesgos: interpretaciones simplistas ("el medio X es sensacionalista"); mostrar siempre los n y los intervalos.

**3. Veto y fusión manual de historias.**
- Datos: `story_overrides(story_key, article_id, action: 'exclude'|'merge_into', target_key, created_at)` aplicado tras el clustering.
- UI: en la página de historia, botón "no es de esta historia" por artículo.
- Riesgos: la clave de historia es inestable entre recálculos; anclar el override al artículo y a un identificador de cluster derivado de sus artículos más antiguos.

**4. Consultas guardadas como feeds.**
- Datos: `saved_queries(id, name, query, filters JSON)`; endpoint `/feeds/q/{id}.atom`.
- UI: botón "suscribirse" en búsqueda; lista en Ajustes.
- Riesgos: respetar el límite de fragmento; token en la URL si la auth está activada.

**5. Posición en portada.**
- Datos: `front_items(shot_id, rank, text, url, bbox JSON, font_size)` extraído durante la captura (DOM).
- UI: sobre la captura, resaltar el titular de apertura; tabla "qué abrió cada medio hoy" y evolución en el día.
- Riesgos: los selectores cambian por medio y se rompen; fallar sin romper la captura y registrar el motivo. Capas de publicidad, banners y *sticky* distorsionan el orden.

### Riesgos transversales
- **Sobreinterpretación**: la plataforma muestra correlaciones léxicas; la UI debe etiquetar qué es hecho (publicó/no publicó), qué es estadística (vocabulario) y qué es estimación de un modelo (tono).
- **Calidad de los feeds**: un RSS que trunca o pierde artículos sesga todas las métricas de omisión y retardo. Registrar cobertura por feed y excluir feeds con baja fiabilidad de los cálculos comparativos.
- **Dependencia del Mac**: toda idea con ✘ debe tener una versión degradada sin LLM.

---

## Lista de fuentes

Fuentes abiertas y leídas por resumen de búsqueda (no todas se abrieron íntegras):

**Productos y proyectos**
- Ground News, ayuda: https://help.ground.news/en/categories/2272705
- Ground News, Blindspot: https://about.ground.news/blindspot
- Ground News, sistema de calificación: https://ground.news/rating-system
- AllSides, métodos: https://www.allsides.com/media-bias/media-bias-rating-methods
- Media Cloud (Harvard): https://cyber.harvard.edu/gsoc/Media_Cloud
- Media Cloud, ICWSM 2021: https://ojs.aaai.org/index.php/ICWSM/article/view/18127
- Media Cloud, arXiv: https://arxiv.org/pdf/2104.03702v2
- GDELT 2.0: https://blog.gdeltproject.org/gdelt-2-0-our-global-world-in-realtime/
- GDELT Translingual: https://blog.gdeltproject.org/?p=1369
- Event Registry, JAIR 2016: https://jair.org/index.php/jair/article/view/10981
- Event Registry, WWW 2014: https://archives.iw3c2.org/www2014/proceedings/companion/p107.pdf
- NewsWhip, predicción: https://www.newswhip.com/prediction
- Google News Full Coverage: https://searchengineland.com/google-brings-full-coverage-news-to-search-results-346727
- NewsDiffs (Wikipedia): https://en.wikipedia.org/wiki/NewsDiffs
- NewsDiffs (Poynter): https://www.poynter.org/news/newsdiffs-tracks-changes-new-york-times-cnn
- FreshRSS Extensions: https://github.com/FreshRSS/Extensions
- Miniflux docs: https://miniflux.app/docs
- ntfy, publicación (no abierta): https://docs.ntfy.sh/publish/
- Maldita.es, IFCN: https://maldita.es/maldita-es-firmante-del-codigo-de-principios-del-international-fact-checking-network/
- Newtral, quiénes somos: https://newtral.es/quienes-somos/

**Métodos**
- Kleinberg, resumen: https://datalearner.com/academic/journal-papers/1384-5810/volumes-and-issues/7/paper-detail/9118
- Kleinberg, KDD 2002: https://bibtex.github.io/KDD-2002-Kleinberg.html
- Rayson y Garside 2000: https://aclanthology.org/W00-0901/
- Monroe, Colaresi y Quinn 2008: https://www.cambridge.org/core/journals/political-analysis/article/fightin-words-lexical-feature-selection-and-evaluation-for-identifying-the-content-of-political-conflict/81B3703230D21620B81EB6E2266C7A66
- Fightin' Words, transparencias JHU: https://nlp-css-601-672.cs.jhu.edu/sp2026/files/0126.LogOdds.pdf
- newsLens (Laban y Hearst 2017): https://aclanthology.org/W17-2701/
- Rupnik et al., arXiv: https://arxiv.org/html/1512.07046
- Ediciones de titulares (NSF PAR): https://par.nsf.gov/servlets/purl/10326932
- Hagar y Diakopoulos 2019: https://doaj.org/article/2583b124fca44a6f90a75d3195139033
- LLM anotador, zero-shot político: https://arxiv.org/pdf/2304.06588
- LLM anotador, pistas partidistas: https://arxiv.org/abs/2408.15895
- LLM anotador, veracidad política: https://arxiv.org/html/2411.05775v1
- Ollama, salidas estructuradas: https://registry.ollama.ai/blog/structured-outputs
- Salida estructurada con Qwen3 y Go (terceros): https://www.glukhov.org/post/2025/09/llm-structured-output-with-ollama-in-python-and-go/
- MarIA, BSC: https://bsc.es/news/bsc-news/first-massive-artificial-intelligence-system-the-spanish-language-maria-begins-summarize-and
- roberta-base-bne: https://huggingface.co/PlanTL-GOB-ES/roberta-base-bne
- imghash (Go): https://pkg.go.dev/github.com/ajdnik/imghash
- goimagehash (referencia): https://docsearch.algolia.com/mcp/docs/repo/corona10/goimagehash

**España: medios y derecho**
- Reuters Institute DNR 2025 España: https://reutersinstitute.politics.ox.ac.uk/digital-news-report/2025/spain
- DNR España, metodología (Navarra): https://en.unav.edu/web/digital-news-report/entradas/-/blogs/metodologia-2
- ACN sobre desconfianza: https://www.catalannews.com/society-science/item/nearly-70-of-spaniards-dont-trust-news-report-finds
- ESADE, polarización de audiencias: https://www.esade.edu/ecpol/es/publicaciones/polarizacion-medios-espana/
- Público, lectores y prensa: https://www.publico.es/sociedad/personas-conservadoras-leen-prensa-les-afin-ideologicamente.html
- Numeroteca, contexto español: https://numeroteca.org/?p=6448
- Clifford Chance, reforma 2014: https://www.cliffordchance.com/content/dam/cliffordchance/briefings/2015/02/reforma-de-la-ley-de-propiedad-intelectual.pdf
- CNMC, nota sobre la tasa: https://www.cnmc.es/sites/default/files/editor_contenidos/Notas%20de%20prensa/2014/20140528_NP_Tasa_Google_def.pdf
- Cuatrecasas, RDL 24/2021: https://www.cuatrecasas.com/es/spain/propiedad-intelectual/art/transposicion-directiva-derechos-de-autor-ii
- Garrigues, RDL 24/2021: https://www.garrigues.com/es_ES/noticia/ocho-claves-reforma-ley-propiedad-intelectual-aprobada-gobierno-real-decreto-ley
- Garrigues, opt-out de minería de datos: https://www.garrigues.com/es_ES/garrigues-digital/ia-derechos-autor-legibilidad-maquina-accionabilidad-maquina-opt-out-tdm
- Ecija, Hamburgo y opt-out técnico: https://www.ecija.com/en/news-and-insights/alemania-el-tribunal-superior-de-hamburgo-refuerza-el-modelo-europeo-de-opt-out-tecnico-en-mineria-de-datos/
- GA-P, Google-Cedro: https://ga-p.com/publicaciones/compensacion-equitativa-por-la-insercion-de-pequenos-fragmentos-de-texto-google-gana-en-primera-instancia-a-cedro/

**Limitaciones de esta investigación.** No se localizaron fuentes primarias para: la metodología propia de MBFC, tendencias tipo Twitter, calidad comparada en español de Qwen3/Gemma/Llama/Phi, colecciones de portadas del Internet Archive, ni prácticas de fatiga de alertas. Esos puntos están marcados como **[sin verificar]** o se resuelven recomendando medición propia.
