// Platforms screen: "new", "series" and "top rated" rows per streaming service, from TMDB's
// watch-provider data (powered by JustWatch) for a region. Loaded by the project's plugin, which sets
// window.media_stack.base. Rows are built with Lampa's own TMDB source, so "more" and the cards behave like
// Lampa's own catalogue. ES5.
(function () {
  'use strict';
  if (window.media_stack_platforms || !window.media_stack || !window.Lampa || !Lampa.Api || !Lampa.Api.sources ||
      !Lampa.Api.sources.tmdb || !Lampa.Api.partNext) return;
  window.media_stack_platforms = true;

  var base = window.media_stack.base;
  var tmdb = Lampa.Api.sources.tmdb;

  Lampa.Lang.add({
    ms_platforms: {ru: 'Платформы', uk: 'Платформи', en: 'Platforms'},
    ms_new: {ru: 'новые фильмы', uk: 'нові фільми', en: 'new films'},
    ms_series: {ru: 'новые сериалы', uk: 'нові серіали', en: 'new series'},
    ms_top: {ru: 'лучшее', uk: 'найкраще', en: 'top rated'},
    ms_region: {ru: 'Регион платформ', uk: 'Регіон платформ', en: 'Platforms region'},
    ms_region_descr: {ru: 'Каталоги сервисов этой страны. Данные: TMDB и JustWatch',
      uk: 'Каталоги сервісів цієї країни. Дані: TMDB і JustWatch',
      en: 'Service catalogues of this country. Data: TMDB and JustWatch'}
  });

  if (Lampa.SettingsApi) {
    Lampa.SettingsApi.addParam({component: 'media_stack',
      param: {name: 'ms_region', type: 'select', 'default': 'PL',
        values: {PL: 'Polska', UA: 'Україна', DE: 'Deutschland', GB: 'United Kingdom', US: 'United States'}},
      field: {name: Lampa.Lang.translate('ms_region'), description: Lampa.Lang.translate('ms_region_descr')}});
  }

  function region() {
    return Lampa.Storage.field('ms_region') || 'PL';
  }

  // The platform list comes from the gateway; TMDB's provider ids for the region are looked up, not hard-coded.
  function resolve(done) {
    $.ajax({url: base + '/platforms.json', dataType: 'json', timeout: 8000,
      success: function (platforms) {
        var ids = {}, pending = 2;
        var finish = function () {
          if (--pending) return;
          var found = [];
          for (var i = 0; i < platforms.length; i++) {
            var re = new RegExp(platforms[i].match);
            for (var name in ids) {
              if (re.test(name)) {
                found.push({name: platforms[i].name, id: ids[name], seriesOnly: !!platforms[i].series_only});
                break;
              }
            }
          }
          done(found);
        };
        ['movie', 'tv'].forEach(function (type) {
          tmdb.get('watch/providers/' + type + '?watch_region=' + region(), {}, function (json) {
            (json.results || []).forEach(function (p) { ids[p.provider_name] = p.provider_id; });
            finish();
          }, finish);
        });
      },
      error: function () { done([]); }
    });
  }

  function parts(found) {
    var today = new Date().toISOString().slice(0, 10);
    var list = [];
    found.forEach(function (p) {
      var q = 'with_watch_providers=' + p.id + '&watch_region=' + region() + '&with_watch_monetization_types=flatrate';
      [['discover/movie?' + q + '&sort_by=primary_release_date.desc&primary_release_date.lte=' + today +
        '&vote_count.gte=5', 'ms_new'],
       ['discover/tv?' + q + '&sort_by=first_air_date.desc&first_air_date.lte=' + today + '&vote_count.gte=5',
        'ms_series'],
       ['discover/movie?' + q + '&sort_by=vote_average.desc&vote_count.gte=300', 'ms_top']
      ].forEach(function (row) {
        // TMDB applies the subscription filter to any provider of a title, not to this one: a service that only
        // rents films (in TMDB's data) would list another service's films. series_only keeps such a service to
        // its series (platforms.json).
        if (p.seriesOnly && row[0].indexOf('discover/movie') === 0) return;
        list.push(function (call) {
          tmdb.get(row[0], {}, function (json) {
            json.title = p.name + ': ' + Lampa.Lang.translate(row[1]);
            call(json);
          }, call);
        });
      });
    });
    return list;
  }

  // A source like Lampa's TMDB one, with its own main screen. Search keeps one TMDB entry: no discovery here.
  var source = {};
  for (var k in tmdb) source[k] = tmdb[k];
  delete source.discovery;
  source.main = function (params, oncomplite, onerror) {
    var rows = null;
    function load(ok, fail) { Lampa.Api.partNext(rows, 6, ok, fail); }
    resolve(function (found) {
      rows = parts(found);
      if (rows.length) load(oncomplite, onerror);
      else onerror();
    });
    return function (ok, fail) {
      if (rows) load(ok, fail);
      else fail();
    };
  };
  Lampa.Api.sources.ms_platforms = source;

  function menu() {
    var item = $('<li class="menu__item selector"><div class="menu__ico"><svg viewBox="0 0 24 24" width="24" ' +
      'height="24"><path fill="currentColor" d="M3 4h18a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1h-7v2h3v2H7v-2h3v-2H3a1 1 0 ' +
      '0 1-1-1V5a1 1 0 0 1 1-1zm1 2v9h16V6H4z"/></svg></div><div class="menu__text">' +
      Lampa.Lang.translate('ms_platforms') + '</div></li>');
    item.on('hover:enter', function () {
      Lampa.Activity.push({url: '', title: Lampa.Lang.translate('ms_platforms'), component: 'main',
        source: 'ms_platforms', page: 1});
    });
    $('.menu .menu__list').eq(0).append(item);
  }
  if (window.appready) menu();
  else Lampa.Listener.follow('app', function (e) { if (e.type === 'ready') menu(); });
})();
