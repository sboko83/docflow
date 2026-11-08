/* docflow roster editor: one table of accesses × plan roles. The state
   comes from GET /api/workspace, every action is POST /api/op with the
   operation names of the roster package, and the workspace in the response
   is the next render. The "default" tab edits roster.toml; every other tab
   is a policy of the variants files — the accesses it switches off, its
   order and the cells it overrides. No build step, no dependencies. */
(function(){
  'use strict';
  var root = document.getElementById('rwe');
  if (!root) return;
  var $ = function(id){ return document.getElementById(id); };

  var JUDGES = { review:true, acceptance:true, replan:true };
  var SRC_RU = { variants:'глобальный файл вариантов', project:'проектный файл вариантов' };
  var SRC_SHORT = { variants:'глобальная', project:'проектная' };
  var INHERIT = '\u0000inherit';

  var ws = null;           // the workspace JSON of the last successful load
  var tab = 'default';     // 'default' or a policy name
  var serverError = '';

  /* ---------- API ---------- */

  function api(payload){
    return fetch('api/op', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    }).then(function(resp){
      return resp.json().then(function(body){ return { status: resp.status, body: body }; });
    }).then(function(result){
      if (result.status === 200 && result.body.ok) {
        ws = result.body.workspace;
        serverError = '';
        if (tab !== 'default' && !policy(tab)) tab = 'default';
        render();
        return true;
      }
      serverError = result.body.error || ('HTTP ' + result.status);
      toast(serverError);
      renderVal();
      return false;
    }).catch(function(err){
      serverError = String(err);
      toast('сервер недоступен: ' + serverError);
      renderVal();
      return false;
    });
  }

  function reload(){
    return fetch('api/workspace').then(function(resp){ return resp.json(); }).then(function(body){
      if (!body.ok || !body.workspace) throw new Error(body.error || 'нет ответа');
      ws = body.workspace;
      serverError = '';
      if (tab !== 'default' && !policy(tab)) tab = 'default';
      render();
    }).catch(function(err){
      serverError = 'ростер не прочитан: ' + String(err);
      renderVal();
    });
  }

  /* ---------- views of the workspace ---------- */

  function roles(){ return ws.role_names || ['executor','cheap','heavy','review','acceptance','replan']; }

  function policy(name){
    var list = ws.variants || [];
    for (var i = 0; i < list.length; i++) if (list[i].name === name) return list[i];
    return null;
  }

  function access(name){
    var list = ws.accesses || [];
    for (var i = 0; i < list.length; i++) if (list[i].access === name) return list[i];
    return null;
  }

  function baseName(path){ return String(path).split(/[\\/]/).pop(); }

  function contains(list, value){ return (list || []).indexOf(value) >= 0; }

  // stateOff reports whether the conditions (roster off) switch an access
  // off, as opposed to a policy.
  function stateOff(a){
    return (a.conditions || []).some(function(c){ return c.kind === 'off' && c.source === 'state'; });
  }

  // table builds the rows the current tab shows: the base cells with the
  // policy overrides on top, in the order of the tab, with the off switch
  // of the policy and the marks of the overridden cells.
  function table(){
    var base = (ws.accesses || []).map(function(a){ return a.access; });
    var p = tab === 'default' ? null : policy(tab);
    var order = base.slice();
    if (p && p.order && p.order.length) {
      order = [];
      p.order.forEach(function(n){ if (contains(base, n) && !contains(order, n)) order.push(n); });
      base.forEach(function(n){ if (!contains(order, n)) order.push(n); });
    }
    return order.map(function(name){
      var a = access(name);
      var cells = {}, overridden = {};
      Object.keys(a.cells || {}).forEach(function(r){ cells[r] = a.cells[r]; });
      var over = p && p.roles && p.roles[name];
      if (over) Object.keys(over).forEach(function(r){ cells[r] = over[r]; overridden[r] = true; });
      return {
        a: a,
        cells: cells,
        overridden: overridden,
        policyOff: !!(p && contains(p.off, name)),
        stateOff: stateOff(a)
      };
    });
  }

  // splitCell reads MODEL@EFFORT: the effort follows the last '@'.
  function splitCell(cell){
    var at = String(cell || '').lastIndexOf('@');
    if (at <= 0) return { model: String(cell || ''), effort: '' };
    return { model: cell.slice(0, at), effort: cell.slice(at + 1) };
  }

  function joinCell(model, effort){ return model ? (effort ? model + '@' + effort : model) : ''; }

  function catalogModel(name, id){
    var models = (ws.catalog_models || {})[name] || [];
    for (var i = 0; i < models.length; i++) if (models[i].id === id) return models[i];
    return null;
  }

  function known(name, cell){ return !!catalogModel(name, splitCell(cell).model); }

  // channelAuth is the auth of one agent channel of an access; an older
  // catalog without per-channel answers gives the access auth.
  function channelAuth(a, agent){
    var c = a.channels || {};
    return Object.keys(c).length ? (c[agent] || '') : (a.auth || '');
  }

  // routeAgent mirrors roster.RouteAgent: the first channel of the access
  // that lists the model and is ready, else the first one not refused.
  // An empty agent comes with the reason the resolver will skip the cell.
  function routeAgent(a, cell){
    var m = catalogModel(a.access, splitCell(cell).model);
    if (!m) return { agent: '', why: 'модели нет в каталоге: docflow roster scan' };
    var listing = (a.agents || []).filter(function(g){ return !(m.agents && m.agents.length) || contains(m.agents, g); });
    if (!listing.length) return { agent: '', why: 'модель знает только ' + (m.agents || []).join(', ') + ', а не каналы доступа' };
    for (var i = 0; i < listing.length; i++) if (channelAuth(a, listing[i]) === 'ready') return { agent: listing[i], why: '' };
    for (var j = 0; j < listing.length; j++) if (channelAuth(a, listing[j]) !== 'not_ready') return { agent: listing[j], why: '' };
    return { agent: '', why: 'нет входа у ' + listing.join(', ') + ' для этой модели' };
  }

  // price renders the per-million price of a model: a known zero is free,
  // an unknown price is blank, never "free".
  function price(m){
    if (!m) return '';
    if (m.free) return 'бесплатно';
    if (!m.input && !m.output) return '';
    return '$' + m.input + '/$' + m.output;
  }

  // levels lists the efforts a model takes: the levels its source declared,
  // or, for a reasoning model without them, the levels its agents accept.
  function levels(name, id){
    var m = catalogModel(name, id);
    if (!m) return { list: [], assumed: false };
    if (m.efforts && m.efforts.length) return { list: m.efforts, assumed: false };
    if (m.reasoning) return { list: m.agent_efforts || [], assumed: true };
    return { list: [], assumed: false };
  }

  // positions numbers the routes of every role in the current tab: the
  // place of each access in the chain of the column, 1 is the primary.
  function positions(rows){
    var out = {};
    roles().forEach(function(role){
      var n = 0;
      out[role] = { count: 0, at: {} };
      rows.forEach(function(row){
        if (row.policyOff || row.stateOff) return;
        if (row.a.check && JUDGES[role]) return;
        if (!row.cells[role] || !routeAgent(row.a, row.cells[role]).agent) return;
        out[role].at[row.a.access] = ++n;
      });
      out[role].count = n;
    });
    return out;
  }

  /* ---------- render ---------- */

  function el(tag, attrs, children){
    var node = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function(k){
      if (k === 'text') node.textContent = attrs[k];
      else if (k === 'cls') node.className = attrs[k];
      else if (k.slice(0, 2) === 'on') node.addEventListener(k.slice(2), attrs[k]);
      else if (attrs[k] === true) node.setAttribute(k, '');
      else if (attrs[k] !== false && attrs[k] != null) node.setAttribute(k, attrs[k]);
    });
    (children || []).forEach(function(c){ if (c) node.appendChild(typeof c === 'string' ? document.createTextNode(c) : c); });
    return node;
  }

  function render(){
    if (!ws) return;
    renderTabs();
    renderPolicyHead();
    renderTable();
    renderChains();
    renderVal();
  }

  function renderVal(){
    var box = $('rwe-val');
    var lines = [];
    if (serverError) lines.push(serverError);
    if (ws && ws.problem) lines.push(ws.problem);
    if (ws && ws.missing) lines.push('ростера ещё нет (' + ws.policy + '): добавьте первый доступ из каталога — файл создастся');
    if (ws && ws.catalog_empty) lines.push('каталог пуст: запустите docflow roster scan');
    box.textContent = lines.join('\n');
    box.classList.toggle('has', lines.length > 0);
  }

  function renderTabs(){
    var box = $('rwe-switch');
    box.innerHTML = '';
    var chip = function(name, label, extra){
      var active = ws.preset ? ws.preset === name : name === 'default';
      var b = el('button', {
        type: 'button', role: 'tab', 'aria-selected': String(tab === name),
        cls: 'rw-chip' + (tab === name ? ' is-active' : '') + (name !== 'default' ? ' is-policy' : ''),
        onclick: function(){ tab = name; render(); }
      }, [label]);
      if (extra) b.appendChild(el('span', { cls: 'rw-chip-src', text: extra }));
      if (active) b.appendChild(el('span', { cls: 'rw-dot', title: 'в силе', 'aria-label': 'в силе' }));
      box.appendChild(b);
    };
    chip('default', 'default');
    (ws.variants || []).forEach(function(v){ chip(v.name, v.name, SRC_SHORT[v.source] || v.source); });
    box.appendChild(el('button', { type: 'button', cls: 'rw-chip rw-chip-add', onclick: openCreatePolicy }, ['+ политика']));
  }

  function renderPolicyHead(){
    var box = $('rwe-polhead');
    box.innerHTML = '';
    var activeName = ws.preset || 'default';
    var isActive = activeName === tab;
    var activate = el('button', {
      type: 'button', cls: 'rw-btn rw-btn-sm' + (isActive ? ' is-on' : ''), disabled: isActive,
      onclick: function(){ api({ op: 'SetPreset', name: tab === 'default' ? '' : tab }); }
    }, [isActive ? 'в силе' : 'сделать активной']);
    if (tab === 'default') {
      box.appendChild(el('div', { cls: 'rw-polinfo' }, [
        el('strong', { text: 'default' }),
        el('span', { cls: 'rw-muted', text: ' — базовая таблица, ' }),
        el('code', { title: ws.policy || '', text: baseName(ws.policy || 'roster.toml') }),
        el('span', { cls: 'rw-muted', text: '. Порядок строк — приоритет доступов. Удалить нельзя.' })
      ]));
      box.appendChild(activate);
      $('rwe-hint').textContent = 'ячейка — модель и ризонинг, которыми доступ закрывает роль; «—» — роль не берёт; номер в кружке — место в цепочке роли, 1 — основной маршрут';
      return;
    }
    var p = policy(tab);
    var name = el('input', { cls: 'rw-vname', value: p.name, 'aria-label': 'Имя политики' });
    name.addEventListener('change', function(){
      var next = name.value.trim();
      if (next && next !== p.name) api({ op: 'RenameVariant', name: p.name, next: next }).then(function(ok){ if (ok) { tab = next; render(); } });
    });
    box.appendChild(el('div', { cls: 'rw-polinfo' }, [
      name,
      el('span', { cls: 'rw-badge', title: p.path || '', text: SRC_RU[p.source] || p.source })
    ]));
    box.appendChild(activate);
    box.appendChild(el('button', {
      type: 'button', cls: 'rw-btn rw-btn-sm rw-danger',
      onclick: function(){
        if (!window.confirm('Удалить политику ' + p.name + '?')) return;
        api({ op: 'DeleteVariant', name: p.name }).then(function(ok){ if (ok) { tab = 'default'; render(); } });
      }
    }, ['удалить']));
    $('rwe-hint').textContent = 'отличия от default подсвечены; «↩ как в default» снимает отличие; порядок и выключение действуют только в этой политике';
  }

  function renderTable(){
    var t = $('rwe-table');
    t.innerHTML = '';
    var rows = table();
    var pos = positions(rows);
    var work = roles().filter(function(r){ return !JUDGES[r]; });
    var judges = roles().filter(function(r){ return JUDGES[r]; });
    var groups = el('tr', { cls: 'rw-groups' }, [
      el('th', { cls: 'rw-th-access', rowspan: '2' }, [
        el('div', { text: 'доступ' }),
        el('div', { cls: 'rw-th-note', text: 'сверху вниз — приоритет' })
      ]),
      el('th', { colspan: String(work.length), cls: 'rw-group', text: 'исполнение' }),
      el('th', { colspan: String(judges.length), cls: 'rw-group is-judge rw-col-first', text: 'судьи' })
    ]);
    var head = el('tr', {});
    roles().forEach(function(r){
      var count = pos[r].count;
      var state = count === 0 ? 'нет маршрута' : count === 1 ? 'нет fallback' : 'маршрутов в цепочке: ' + count;
      head.appendChild(el('th', { cls: (JUDGES[r] ? 'is-judge' : '') + (r === judges[0] ? ' rw-col-first' : ''), title: state }, [
        el('span', { text: r }),
        el('span', { cls: 'rw-th-count' + (count < 2 ? ' is-warn' : ''), text: count < 2 ? '⚠ ' + count : '×' + count })
      ]));
    });
    t.appendChild(el('thead', {}, [groups, head]));
    var body = el('tbody');
    rows.forEach(function(row, index){
      var off = row.policyOff || row.stateOff;
      var tr = el('tr', { cls: off ? 'is-off' : '' });
      tr.appendChild(el('th', { cls: 'rw-td-access', scope: 'row' }, [accessCell(row, index, rows.length)]));
      roles().forEach(function(r){
        tr.appendChild(el('td', { cls: 'rw-td-cell' + (r === judges[0] ? ' rw-col-first' : '') }, [cellSelect(row, r, pos[r].at[row.a.access])]));
      });
      body.appendChild(tr);
    });
    if (!rows.length) {
      body.appendChild(el('tr', {}, [el('td', { colspan: String(roles().length + 1), cls: 'rw-empty', text: 'нет доступов: добавьте из каталога' })]));
    }
    t.appendChild(body);
    var left = (ws.catalog_accesses || []).filter(function(c){ return !access(c.access); }).map(function(c){ return c.access; });
    $('rwe-catalog-left').textContent = left.length ? 'ещё в каталоге: ' + left.join(', ') : '';
    $('rwe-add-access').hidden = tab !== 'default';
  }

  var KINDS = ['subscription', 'metered', 'local'];
  var KIND_RU = { subscription: 'подписка', metered: 'за токены', local: 'локально' };
  var AUTH_RU = { ready: 'вход есть', not_ready: 'нет входа', unknown: 'вход не проверен' };

  function accessCell(row, index, count){
    var a = row.a;
    var move = function(dir){
      return function(){
        if (tab === 'default') api({ op: 'MoveAccess', name: a.access, dir: dir });
        else api({ op: 'MoveVariantAccess', name: tab, access: a.access, dir: dir });
      };
    };
    var box = el('div', { cls: 'rw-acc' });
    box.appendChild(el('div', { cls: 'rw-move' }, [
      el('button', { type: 'button', cls: 'rw-icon', title: 'выше по приоритету', 'aria-label': a.access + ' выше', disabled: index === 0, onclick: move('up') }, ['▲']),
      el('span', { cls: 'rw-rank', text: String(index + 1) }),
      el('button', { type: 'button', cls: 'rw-icon', title: 'ниже по приоритету', 'aria-label': a.access + ' ниже', disabled: index === count - 1, onclick: move('down') }, ['▼'])
    ]));
    var meta = el('div', { cls: 'rw-accmeta' });
    var title = el('div', { cls: 'rw-accname' }, [el('span', { text: a.access })]);
    if (a.auth && a.auth !== 'ready') title.appendChild(el('span', { cls: 'rw-tag rw-tag-warn', text: AUTH_RU[a.auth] || ('auth ' + a.auth) }));
    if (row.stateOff) title.appendChild(el('span', { cls: 'rw-tag rw-tag-warn', title: 'roster off — условие, снимается roster on', text: 'off' + (a.until ? ' до ' + a.until : '') }));
    meta.appendChild(title);
    var kindText = KIND_RU[a.kind];
    if (kindText && a.policy_kind && a.catalog_kind && a.policy_kind !== a.catalog_kind) kindText += ' (скан: ' + KIND_RU[a.catalog_kind] + ')';
    var sub = [a.title, kindText].filter(Boolean).join(' · ');
    if (sub) meta.appendChild(el('div', { cls: 'rw-acctitle', title: 'провайдер и тип оплаты доступа', text: sub }));
    meta.appendChild(agentLine(a));
    var flags = el('div', { cls: 'rw-flags' });
    if (tab === 'default') {
      title.appendChild(el('button', {
        type: 'button', cls: 'rw-icon rw-del', title: 'удалить доступ из ростера', 'aria-label': 'удалить ' + a.access,
        onclick: function(){ if (window.confirm('Удалить доступ ' + a.access + ' из ростера? Его ячейки пропадут и во всех политиках.')) api({ op: 'RemoveAccess', name: a.access }); }
      }, ['✕']));
      var differs = a.policy_kind && a.catalog_kind && a.policy_kind !== a.catalog_kind;
      var kind = el('select', {
        cls: 'rw-mini' + (differs ? ' is-warn' : ''),
        title: 'тип оплаты: подписка — квота плана, за токены — оплата по API, локально — своя машина без квоты; «авто» — как определил roster scan' + (differs ? '. Скан считает: ' + KIND_RU[a.catalog_kind] : ''),
        'aria-label': a.access + ': тип оплаты'
      }, [el('option', { value: '', text: 'авто: ' + (KIND_RU[a.catalog_kind] || 'не знаю') })].concat(KINDS.map(function(k){ return el('option', { value: k, text: KIND_RU[k] }); })));
      kind.value = a.policy_kind || '';
      kind.addEventListener('change', function(){ api({ op: 'SetAccessKind', name: a.access, kind: kind.value }); });
      flags.appendChild(kind);
      flags.appendChild(checkbox('check', a.check, 'нужен независимый ревьюер: доступ не судит (review, acceptance, replan)', function(){
        api({ op: 'SetAccessCheck', name: a.access, check: !a.check });
      }, true));
    } else {
      if (a.check) flags.appendChild(el('span', { cls: 'rw-tag', text: 'check' }));
      flags.appendChild(checkbox('в политике', !row.policyOff, 'снимите, чтобы выключить доступ только в этой политике', function(){
        api({ op: 'SetVariantOff', name: tab, access: a.access, off: !row.policyOff });
      }));
    }
    meta.appendChild(flags);
    box.appendChild(meta);
    return box;
  }

  // agentLine shows the agent channels of an access with the login state
  // of each. A route runs through the first channel that has a login and
  // knows the model, so with two channels the editor picks the first.
  function agentLine(a){
    var agents = a.agents || [];
    var line = el('div', { cls: 'rw-agents' });
    if (agents.length > 1 && tab === 'default') {
      var pick = el('select', { cls: 'rw-mini', title: 'агент, через который идёт маршрут; нет у него входа или он не знает модель — следующий', 'aria-label': a.access + ': агент' },
        agents.map(function(g){ return el('option', { value: g, text: 'через ' + g }); }));
      pick.value = agents[0];
      pick.addEventListener('change', function(){ api({ op: 'SetAccessAgent', name: a.access, agent: pick.value }); });
      line.appendChild(pick);
    } else {
      line.appendChild(el('span', { cls: 'rw-muted', text: 'через' }));
    }
    agents.forEach(function(g){
      var st = channelAuth(a, g);
      line.appendChild(el('span', {
        cls: 'rw-chan' + (st === 'not_ready' ? ' is-bad' : st === 'ready' ? ' is-ok' : ''),
        title: g + ': ' + (AUTH_RU[st] || 'вход не проверен')
      }, [g + (st === 'not_ready' ? ' ✗' : st === 'ready' ? ' ✓' : ' ?')]));
    });
    return line;
  }

  function checkbox(label, on, title, handler, warn){
    var input = el('input', { type: 'checkbox' });
    input.checked = !!on;
    input.addEventListener('change', handler);
    return el('label', { cls: 'rw-cbox' + (on && warn ? ' is-on' : ''), title: title }, [input, label]);
  }

  // cellSelect is the editor of one cell: the model and, below it, the
  // effort. A reasoning model without declared levels offers the levels
  // of its agents; an effort outside the offered list stays selectable.
  function cellSelect(row, role, place){
    var a = row.a;
    if (a.check && JUDGES[role]) return el('span', { cls: 'rw-cell-na', title: 'check-доступ не судит', text: 'не судит' });
    var value = row.cells[role] || '';
    var inheriting = tab !== 'default' && !row.overridden[role];
    var parts = splitCell(value);
    var catalog = (ws.catalog_models || {})[a.access] || [];
    var models = catalog.map(function(m){ return m.id; });
    // An option names the price when the access pays per token (or its
    // kind is unknown) and says when no channel of the access can run it.
    var label = function(m){
      var bits = [m.id];
      if (a.kind !== 'subscription' && a.kind !== 'local') { var p = price(m); if (p) bits.push(p); }
      if (!routeAgent(a, m.id).agent) bits.push('не запустить');
      return bits.join(' · ');
    };
    var write = function(next){
      if (tab === 'default') api({ op: 'SetCell', access: a.access, role: role, value: next });
      else if (next === INHERIT) api({ op: 'SetVariantCell', name: tab, access: a.access, role: role, inherit: true });
      else api({ op: 'SetVariantCell', name: tab, access: a.access, role: role, value: next });
    };

    var model = el('select', { cls: 'rw-select' + (value ? '' : ' is-empty') + (row.overridden[role] ? ' is-over' : ''), 'aria-label': a.access + ' ' + role + ': модель' });
    if (tab !== 'default') model.appendChild(el('option', { value: INHERIT, text: '↩ как в default' }));
    model.appendChild(el('option', { value: '', text: '—' }));
    if (parts.model && !contains(models, parts.model)) model.appendChild(el('option', { value: parts.model, text: parts.model + ' · нет в каталоге' }));
    catalog.forEach(function(m){ model.appendChild(el('option', { value: m.id, text: label(m) })); });
    model.value = inheriting ? INHERIT : parts.model;
    model.addEventListener('change', function(){
      var id = model.value;
      if (id === INHERIT || id === '') { write(id); return; }
      var offered = levels(a.access, id).list;
      var effort = contains(offered, parts.effort) ? parts.effort : (contains(offered, 'high') ? 'high' : '');
      write(joinCell(id, effort));
    });

    var wrap = el('div', { cls: 'rw-cell' });
    var line = el('div', { cls: 'rw-cell-line' });
    if (place) line.appendChild(el('span', { cls: 'rw-pos' + (place === 1 ? ' is-primary' : ''), title: place === 1 ? 'основной маршрут роли' : 'fallback №' + (place - 1), text: String(place) }));
    line.appendChild(model);
    var reach = value ? routeAgent(a, value) : { agent: '' };
    if (value && !reach.agent) line.appendChild(el('span', { cls: 'rw-cell-warn', title: reach.why + '; резолвер пропустит ячейку', text: '!' }));
    wrap.appendChild(line);
    if (value && reach.agent && (a.agents || []).length > 1 && reach.agent !== a.agents[0]) {
      wrap.appendChild(el('div', { cls: 'rw-sub', title: 'первый канал не годится для этой модели', text: 'через ' + reach.agent }));
    }

    if (inheriting) {
      wrap.appendChild(el('div', { cls: 'rw-sub', title: 'значение default', text: value ? '= ' + value : '= пусто' }));
      return wrap;
    }
    if (!parts.model) return wrap;
    var lv = levels(a.access, parts.model);
    if (!lv.list.length && !parts.effort) {
      if (known(a.access, value)) wrap.appendChild(el('div', { cls: 'rw-sub', text: 'без ризонинга' }));
      return wrap;
    }
    var effort = el('select', { cls: 'rw-effort' + (row.overridden[role] ? ' is-over' : ''), 'aria-label': a.access + ' ' + role + ': ризонинг', title: lv.assumed ? 'каталог не перечислил уровни модели: показаны уровни её агента' : 'уровень ризонинга' });
    effort.appendChild(el('option', { value: '', text: 'по умолчанию' }));
    if (parts.effort && !contains(lv.list, parts.effort)) effort.appendChild(el('option', { value: parts.effort, text: parts.effort + ' · не в списке модели' }));
    lv.list.forEach(function(e){ effort.appendChild(el('option', { value: e, text: e + (lv.assumed ? ' · уровень агента' : '') })); });
    effort.value = parts.effort;
    effort.addEventListener('change', function(){ write(joinCell(parts.model, effort.value)); });
    wrap.appendChild(el('div', { cls: 'rw-cell-line' }, [place ? el('span', { cls: 'rw-pos-pad' }) : null, effort]));
    return wrap;
  }

  function renderChains(){
    var box = $('rwe-chains');
    box.innerHTML = '';
    var rows = table();
    var warnings = [];
    roles().forEach(function(role){
      var routes = [];
      rows.forEach(function(row){
        if (row.policyOff || row.stateOff) return;
        if (row.a.check && JUDGES[role]) return;
        var cell = row.cells[role];
        if (!cell) return;
        var reach = routeAgent(row.a, cell);
        routes.push({ text: row.a.access + '/' + cell + (reach.agent ? ' · ' + reach.agent : ''), missing: !reach.agent, why: reach.why });
      });
      var line = el('div', { cls: 'rw-chain' }, [el('span', { cls: 'rw-chain-role', text: role })]);
      // The primary is the first route the resolver keeps: a skipped
      // cell stays visible, struck out, but is neither primary nor counted.
      var live = routes.filter(function(r){ return !r.missing; }).length;
      var primary = -1;
      routes.some(function(r, i){ if (!r.missing) { primary = i; return true; } return false; });
      if (!live) {
        line.appendChild(el('span', { cls: 'rw-chain-none', text: 'нет маршрута' }));
        warnings.push('нет маршрута для ' + role);
      }
      routes.forEach(function(r, i){
        if (i) line.appendChild(el('span', { cls: 'rw-chain-sep', text: '›' }));
        line.appendChild(el('span', { cls: 'rw-route' + (i === primary ? ' is-primary' : '') + (r.missing ? ' is-missing' : ''), title: r.missing ? r.why + '; резолвер пропустит маршрут' : '', text: r.text }));
      });
      if (live === 1) warnings.push('нет fallback для ' + role);
      box.appendChild(line);
    });
    var w = $('rwe-warnings');
    w.textContent = warnings.length ? '⚠ ' + warnings.join(' · ') : '';
  }

  /* ---------- dialogs ---------- */

  function openModal(build){
    var modal = $('rwe-modal');
    modal.innerHTML = '';
    build(modal);
    $('rwe-overlay').classList.add('is-open');
    var first = modal.querySelector('input, select, button');
    if (first) first.focus();
  }

  function closeModal(){ $('rwe-overlay').classList.remove('is-open'); }

  function openCreatePolicy(){
    openModal(function(modal){
      var name = el('input', { cls: 'rw-input', placeholder: 'например zai-first', 'aria-label': 'Имя политики' });
      var project = el('input', { type: 'checkbox', id: 'rwe-project' });
      var copy = el('select', { cls: 'rw-input', 'aria-label': 'Копия' }, [el('option', { value: '', text: 'пустая — как default' })]);
      (ws.variants || []).forEach(function(v){ copy.appendChild(el('option', { value: v.name, text: 'копия ' + v.name })); });
      var submit = function(){
        var value = name.value.trim();
        if (!value) return;
        api({ op: 'CreateVariant', name: value, project: project.checked, copy_of: copy.value }).then(function(ok){
          if (ok) { tab = value; closeModal(); render(); }
        });
      };
      name.addEventListener('keydown', function(e){ if (e.key === 'Enter') submit(); });
      modal.appendChild(el('h2', { text: 'Новая политика' }));
      modal.appendChild(el('p', { cls: 'rw-muted', text: 'Политика меняет default: выключает доступы, меняет их порядок и отдельные ячейки.' }));
      modal.appendChild(name);
      modal.appendChild(copy);
      modal.appendChild(el('label', { cls: 'rw-check', for: 'rwe-project' }, [project, ' в проект (docs/roster.variants.toml, коммитится)']));
      modal.appendChild(el('div', { cls: 'rw-modal-actions' }, [
        el('button', { type: 'button', cls: 'rw-btn', onclick: closeModal }, ['отмена']),
        el('button', { type: 'button', cls: 'rw-btn rw-btn-primary', onclick: submit }, ['создать'])
      ]));
    });
  }

  function openAddAccess(){
    var left = (ws.catalog_accesses || []).filter(function(c){ return !access(c.access); });
    openModal(function(modal){
      modal.appendChild(el('h2', { text: 'Доступ из каталога' }));
      if (!left.length) modal.appendChild(el('p', { cls: 'rw-muted', text: 'Все доступы каталога уже в ростере. docflow roster scan найдёт новые.' }));
      left.forEach(function(c){
        modal.appendChild(el('button', {
          type: 'button', cls: 'rw-pick',
          onclick: function(){ api({ op: 'AddAccess', name: c.access, kind: '', agents: c.agents || [] }).then(function(ok){ if (ok) closeModal(); }); }
        }, [el('strong', { text: c.access }), el('span', { cls: 'rw-muted', text: ' ' + [c.title, KIND_RU[c.kind] || 'тип не известен', 'через ' + (c.agents || []).join(', '), 'моделей ' + c.models].filter(Boolean).join(' · ') })]));
      });
      modal.appendChild(el('div', { cls: 'rw-modal-actions' }, [el('button', { type: 'button', cls: 'rw-btn', onclick: closeModal }, ['закрыть'])]));
    });
  }

  function toast(text){
    var box = $('rwe-toasts');
    var item = el('div', { cls: 'rw-toast', text: text });
    box.appendChild(item);
    setTimeout(function(){ item.remove(); }, 6000);
  }

  $('rwe-reload').addEventListener('click', reload);
  $('rwe-add-access').addEventListener('click', openAddAccess);
  $('rwe-overlay').addEventListener('click', function(e){ if (e.target === $('rwe-overlay')) closeModal(); });
  document.addEventListener('keydown', function(e){ if (e.key === 'Escape') closeModal(); });
  reload();
})();
