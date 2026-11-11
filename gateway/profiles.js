// "Who is watching?": a Netflix-style profile picker before the catalogue, with adding, renaming and
// removing profiles. The list lives on the gateway, shared by every device; Lampac keeps each profile's
// bookmarks and positions under lampac_profile_id. Loaded by the project's plugin, which hands over
// loadSync(): Lampac's sync scripts start only once the profile is known. ES5.
(function () {
  'use strict';
  var ms = window.media_stack;
  if (!ms || window.media_stack_profiles) return;
  window.media_stack_profiles = true;

  var base = ms.base;
  var list = [];
  var layer = null;
  var startup = true;
  var busy = false; // a keyboard or menu opened from the picker has the remote
  var palette = ['#e50914', '#1f80e0', '#2e9e5b', '#d4a017', '#8e44ad', '#e67e22', '#16a085', '#c2185b'];

  Lampa.Lang.add({
    ms_who: {ru: 'Кто смотрит?', uk: 'Хто дивиться?', en: "Who's watching?"},
    ms_main: {ru: 'Общий', uk: 'Спільний', en: 'Shared'},
    ms_add: {ru: 'Добавить', uk: 'Додати', en: 'Add'},
    ms_edit: {ru: 'Изменить', uk: 'Змінити', en: 'Edit'},
    ms_name: {ru: 'Имя профиля', uk: 'Імʼя профілю', en: 'Profile name'},
    ms_rename: {ru: 'Переименовать', uk: 'Перейменувати', en: 'Rename'},
    ms_remove: {ru: 'Удалить профиль', uk: 'Видалити профіль', en: 'Remove profile'},
    ms_failed: {ru: 'Не удалось сохранить профиль', uk: 'Не вдалося зберегти профіль', en: 'Could not save the profile'},
    ms_ask: {ru: 'Выбор профиля при запуске', uk: 'Вибір профілю під час запуску', en: 'Pick a profile at start'},
    ms_ask_descr: {ru: 'Экран «Кто смотрит?», когда профилей больше одного',
      uk: 'Екран «Хто дивиться?», коли профілів більше одного', en: "The \"Who's watching?\" screen when there are several profiles"}
  });
  function t(key) { return Lampa.Lang.translate(key); }

  function current() {
    var v = Lampa.Storage.get('lampac_profile_id', '');
    return v === 0 || v === '0' || !v ? '' : String(v);
  }
  function nameOf(p) { return p.name || t('ms_main'); }

  function loadSync() {
    if (!startup) return;
    startup = false;
    ms.loadSync();
  }

  function api(body, done) {
    $.ajax({url: base + '/profiles', type: body ? 'POST' : 'GET', data: body ? JSON.stringify(body) : undefined,
      contentType: 'text/plain', dataType: 'json', timeout: 8000,
      success: function (l) { list = l || []; done(true); },
      error: function () {
        if (body) Lampa.Noty.show(t('ms_failed'));
        done(false);
      }});
  }

  // The device keeps bookmarks and positions without a profile; Lampac holds every profile's copy. So a switch
  // clears the local ones and restarts Lampa, whose sync then pulls the new profile's data.
  function choose(id) {
    if (id === current()) {
      close();
      loadSync();
      return;
    }
    Lampa.Storage.set('favorite', {});
    Lampa.Storage.set('file_view', {});
    Lampa.Storage.set('lampac_profile_id', id);
    try { sessionStorage.setItem('ms_profile_chosen', '1'); } catch (e) {}
    window.location.reload();
  }

  function tile(letter, name, color, onEnter, extra) {
    var el = $('<div class="ms-profile selector"><div class="ms-profile__avatar"><span></span></div>' +
      '<div class="ms-profile__name"></div></div>');
    el.find('span').text(letter);
    el.find('.ms-profile__name').text(name);
    if (color) el.find('.ms-profile__avatar').css('background', color);
    if (extra) el.addClass(extra);
    el.on('hover:enter', onEnter);
    return el;
  }

  function render() {
    busy = false;
    if (layer) layer.remove();
    layer = $('<div class="ms-profiles"><div class="ms-profiles__title"></div><div class="ms-profiles__list"></div></div>');
    layer.find('.ms-profiles__title').text(t('ms_who'));
    var box = layer.find('.ms-profiles__list');
    list.forEach(function (p, i) {
      box.append(tile(nameOf(p).charAt(0).toUpperCase(), nameOf(p), palette[i % palette.length],
        function () { choose(p.id); }, p.id === current() ? 'ms-profile--current' : ''));
    });
    if (list.length < 8) box.append(tile('+', t('ms_add'), '', add, 'ms-profile--action'));
    box.append(tile('✎', t('ms_edit'), '', edit, 'ms-profile--action'));
    $('body').append(layer);
    Lampa.Controller.add('ms_profiles', {
      toggle: function () { // also where the keyboard's or menu's "back" returns to
        busy = false;
        Lampa.Controller.collectionSet(layer);
        Lampa.Controller.collectionFocus(layer.find('.ms-profile--current')[0] || false, layer);
      },
      left: function () { Navigator.move('left'); },
      right: function () { Navigator.move('right'); },
      up: function () { Navigator.move('up'); },
      down: function () { Navigator.move('down'); },
      back: function () { choose(current()); } // keep the current profile
    });
    // Not a Lampa "window" (Controller.windowOpen): Lampa closes those by their back when the catalogue
    // starts, which would dismiss the picker at once. The keyboard and menus come back through render().
    Lampa.Controller.toggle('ms_profiles');
  }

  if (Lampa.Controller.listener) {
    Lampa.Controller.listener.follow('toggle', function (e) {
      if (layer && !busy && e.name !== 'ms_profiles') setTimeout(function () {
        if (layer && !busy) Lampa.Controller.toggle('ms_profiles');
      }, 0);
    });
  }

  function close() {
    if (!layer) return;
    layer.remove();
    layer = null;
    Lampa.Controller.toggle('content');
  }

  // The picker sits above the catalogue and top bar (z-index 15) and below Lampa's keyboard (21) and menus
  // (55), so they open over it; while they are up the remote is theirs.
  function away() {
    busy = true;
  }

  function ask(title, value, done) {
    away();
    Lampa.Input.edit({title: title, value: value || '', free: true, nosave: true}, function (name) {
      name = (name || '').trim();
      if (name) done(name);
      else render(); // "Cancel" empties the value first
    });
  }

  function add() {
    ask(t('ms_name'), '', function (name) { api({action: 'add', name: name}, render); });
  }

  function edit() {
    away();
    Lampa.Select.show({title: t('ms_edit'), items: list.map(function (p) { return {title: nameOf(p), id: p.id}; }),
      onBack: render,
      onSelect: function (item) {
        var actions = [{title: t('ms_rename'), act: 'rename'}];
        if (item.id) actions.push({title: t('ms_remove'), act: 'remove'});
        Lampa.Select.show({title: item.title, items: actions, onBack: render,
          onSelect: function (a) {
            if (a.act === 'rename') {
              ask(t('ms_name'), item.id ? item.title : '', function (name) {
                api({action: 'rename', id: item.id, name: name}, render);
              });
            } else {
              api({action: 'remove', id: item.id}, function () {
                if (item.id === current()) choose('');
                else render();
              });
            }
          }});
      }});
  }

  function start() {
    var chosen = false;
    try {
      chosen = sessionStorage.getItem('ms_profile_chosen') === '1';
      sessionStorage.removeItem('ms_profile_chosen');
    } catch (e) {}
    api(null, function (ok) {
      try {
        if (ok && current() && !list.some(function (p) { return p.id === current(); })) return choose(''); // removed
        var open = function () { api(null, render); };
        var icon = Lampa.Head.addIcon('<svg viewBox="0 0 24 24" width="24" height="24"><path fill="currentColor" ' +
          'd="M12 12a5 5 0 1 0 0-10 5 5 0 0 0 0 10zm0 2c-4.4 0-8 2.2-8 5v3h16v-3c0-2.8-3.6-5-8-5z"/></svg>', open);
        // Lampa binds the mouse to top-bar items only when the remote first visits the bar; until then, ours.
        icon[0].addEventListener('click', function () { if (!icon[0].bind_events) open(); });
        if (Lampa.SettingsApi) {
          Lampa.SettingsApi.addParam({component: 'media_stack', param: {name: 'ms_profile_ask', type: 'trigger', 'default': true},
            field: {name: t('ms_ask'), description: t('ms_ask_descr')}});
        }
        if (!ok || list.length < 2 || chosen || Lampa.Storage.field('ms_profile_ask') === false) return loadSync();
        render();
      } catch (e) {
        console.error('media-stack profiles', e);
        loadSync();
      }
    });
  }

  $('head').append('<style>' +
    '.ms-profiles{position:fixed;top:0;left:0;right:0;bottom:0;z-index:17;background:#141414;display:flex;' +
    'flex-direction:column;align-items:center;justify-content:center}' +
    '.ms-profiles__title{font-size:3em;color:#fff;margin-bottom:1em}' +
    '.ms-profiles__list{display:flex;flex-wrap:wrap;justify-content:center;max-width:90%}' +
    '.ms-profile{display:flex;flex-direction:column;align-items:center;width:9em;margin:0 1em 2em}' +
    '.ms-profile__avatar{width:7em;height:7em;border-radius:.5em;display:flex;align-items:center;' +
    'justify-content:center;color:#fff;font-weight:700;background:#333;transition:transform .15s}' +
    '.ms-profile__avatar span{font-size:3.2em}' +
    '.ms-profile__name{margin-top:.7em;color:#999;font-size:1.3em;text-align:center}' +
    '.ms-profile.focus .ms-profile__avatar{box-shadow:0 0 0 .25em #fff;transform:scale(1.08)}' +
    '.ms-profile.focus .ms-profile__name,.ms-profile--current .ms-profile__name{color:#fff}' +
    '.ms-profile--action .ms-profile__avatar{background:transparent;border:.15em solid #555;color:#999}' +
    '</style>');

  if (window.appready) start();
  else Lampa.Listener.follow('app', function (e) { if (e.type === 'ready') start(); });
})();
