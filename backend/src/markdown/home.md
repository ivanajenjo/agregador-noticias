# Programa agregador de noticias creado por Iván Ajenjo

Este programa va a realizar capturas de pantalla y scraping de ciertos periodicos españoles, y las va a guardar en la base de datos. El programa está totalmente desarrollado utilizando Typescript, sin embargo se ejecuta utilizando NodeJs en lugar de Deno. Por tanto Typescript es compilado a Javascript.

Por lo tanto el programa hará lo siguiente:

1. Realizar capturas de pantalla de los periódicos.
1. Guardar las capturas en la base de datos.
1. Obtener los titulares de estos.

Más adelante esta api devolverá los resultados obtenidos a nuestro FrontEnd. Por tanto se podrá seleccionar un dia y obtendremos las capturas de pantalla y los titulares que se escribieron ese dia. Así podremos comparar los titulares de las noticias de periódicos de distintas ideologias. Las capturas y toda la informacion necesaria se guarda en una base de datos de mongodb.

Más adelante se plantea realizar un port de toda la aplicacion para dockerizarla tanto la base de datos como el ejecutable.