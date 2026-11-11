# 4. TMDB API key

The catalogue (posters, descriptions, ratings, cast, trailers) comes from TMDB. The gateway uses **your own**
key, kept on the server, and caches responses.

1. Create an account at [themoviedb.org](https://www.themoviedb.org/), or sign in.
2. Open your account settings and click **API** in the left-hand sidebar, then apply for an API key
   (TMDB's [FAQ](https://developer.themoviedb.org/docs/faq)).
3. Copy the **API Key** (the short key used as `api_key`). The longer **API Read Access Token** works too.
4. Put it into `.env` as `TMDB_API_KEY` ([step 5](05-clone-and-configure.md)); in Portainer, as a stack variable.

Notes:

* TMDB documents a rate limit of roughly 40 requests per second
  ([rate limiting](https://developer.themoviedb.org/docs/rate-limiting)). The gateway's cache keeps a
  household far below it.
* The key never reaches your TVs or phones. They call the gateway, and the gateway calls TMDB.
* Without a key the stack still works: Lampa then calls TMDB itself with its built-in key, as stock Lampa does,
  with no cache and no language fallback.
* Stock TV apps (Tizen, webOS) may route TMDB through CUB's mirrors on their own when they think they are in
  Russia; the browser build and the Android app with the gateway's Lampa address do not.
