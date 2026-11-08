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

  // options lists the cells a select offers for an access: every catalog
  // model at every effort it declares, the bare id for a model without
  // efforts.
  function options(name){
    var list = [];
    var models = (ws.catalog_models || {})[name] || [];
    models.forEach(function(m){
      if (m.efforts && m.efforts.length) {
        m.efforts.forEach(function(e){ list.push(m.id + '@' + e); });
      } else {
        list.push(m.id);
      }
    });
    return list;
  }

  function known(name, cell){
    var id = cell.split('@')[0];
    if (cell.lastIndexOf('@') > 0) id = cell.slice(0, cell.lastIndexOf('@'));
    var models = (ws.catalog_models || {})[name] || [];
    return models.some(function(m){ return m.id === id; });
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
    if (ws && ws.missing) lines.push('нет ростера: ' + ws.policy);
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
      $('rwe-hint').textContent = 'ячейка — модель@ризонинг, которой доступ закрывает роль; «—» — доступ роль не берёт';
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
    $('rwe-hint').textContent = 'отличия от default подсвечены; «как в default» снимает отличие; порядок и выключение — только для этой политики';
  }

  function renderTable(){
    var t = $('rwe-table');
    t.innerHTML = '';
    var rows = table();
    var head = el('tr', {}, [el('th', { cls: 'rw-th-access', text: 'доступ' })]);
    roles().forEach(function(r){ head.appendChild(el('th', { text: r, cls: JUDGES[r] ? 'is-judge' : '' })); });
    t.appendChild(el('thead', {}, [head]));
    var body = el('tbody');
    rows.forEach(function(row, index){
      var a = row.a;
      var off = row.policyOff || row.stateOff;
      var tr = el('tr', { cls: off ? 'is-off' : '' });
      tr.appendChild(el('td', { cls: 'rw-td-access' }, [accessCell(row, index, rows.length)]));
      roles().forEach(function(r){ tr.appendChild(el('td', { cls: 'rw-td-cell' }, [cellSelect(row, r)])); });
      body.appendChild(tr);
    });
    if (!rows.length) {
      body.appendChild(el('tr', {}, [el('td', { colspan: String(roles().length + 1), cls: 'rw-empty', text: 'нет доступов: добавьте из каталога' })]));
    }
    t.appendChild(body);
    var left = (ws.catalog_accesses || []).filter(function(c){ return !access(c.access); }).map(function(c){ return c.access; });
    $('rwe-catalog-left').textContent = left.length ? 'в каталоге есть: ' + left.join(', ') : '';
    $('rwe-add-access').hidden = tab !== 'default';
  }

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
      el('button', { type: 'button', cls: 'rw-icon', title: 'выше', 'aria-label': 'выше', disabled: index === 0, onclick: move('up') }, ['▲']),
      el('button', { type: 'button', cls: 'rw-icon', title: 'ниже', 'aria-label': 'ниже', disabled: index === count - 1, onclick: move('down') }, ['▼'])
    ]));
    var meta = el('div', { cls: 'rw-accmeta' });
    var title = el('div', { cls: 'rw-accname' }, [el('span', { text: a.access })]);
    if (a.kind) title.appendChild(el('span', { cls: 'rw-tag', text: a.kind }));
    if (a.auth && a.auth !== 'ready') title.appendChild(el('span', { cls: 'rw-tag rw-tag-warn', text: 'auth ' + a.auth }));
    meta.appendChild(title);
    var flags = el('div', { cls: 'rw-flags' });
    if (tab === 'default') {
      flags.appendChild(toggle('check', a.check, 'нужен независимый ревьюер; доступ не судит', function(){
        api({ op: 'SetAccessCheck', name: a.access, check: !a.check });
      }));
      if (row.stateOff) flags.appendChild(el('span', { cls: 'rw-tag rw-tag-warn', title: 'roster off — условие, снимается roster on', text: 'off' + (a.until ? ' до ' + a.until : '') }));
      flags.appendChild(el('button', {
        type: 'button', cls: 'rw-link', title: 'удалить доступ из ростера',
        onclick: function(){ if (window.confirm('Удалить доступ ' + a.access + ' из ростера?')) api({ op: 'RemoveAccess', name: a.access }); }
      }, ['удалить']));
    } else {
      if (a.check) flags.appendChild(el('span', { cls: 'rw-tag', text: 'check' }));
      if (row.stateOff) flags.appendChild(el('span', { cls: 'rw-tag rw-tag-warn', title: 'roster off — условие, снимается roster on', text: 'off' }));
      flags.appendChild(toggle(row.policyOff ? 'выключен' : 'выключить', row.policyOff,'выключить доступ в этой политике', function(){
        api({ op: 'SetVariantOff', name: tab, access: a.access, off: !row.policyOff });
      }));
    }
    meta.appendChild(flags);
    box.appendChild(meta);
    return box;
  }

  function toggle(label, on, title, handler){
    return el('button', { type: 'button', cls: 'rw-toggle' + (on ? ' is-on' : ''), 'aria-pressed': String(!!on), title: title, onclick: handler }, [label]);
  }

  function cellSelect(row, role){
    var a = row.a;
    if (a.check && JUDGES[role]) return el('span', { cls: 'rw-cell-na', title: 'check-доступ не судит', text: 'не судит' });
    var value = row.cells[role] || '';
    var list = options(a.access);
    var select = el('select', { cls: 'rw-select' + (value ? '' : ' is-empty') + (row.overridden[role] ? ' is-over' : ''), 'aria-label': a.access + ' ' + role });
    if (tab !== 'default') {
      var inherited = a.cells[role] || '—';
      select.appendChild(el('option', { value: INHERIT, text: '↩ ' + inherited + ' (default)' }));
    }
    select.appendChild(el('option', { value: '', text: '—' }));
    if (value && !contains(list, value)) list = [value].concat(list);
    list.forEach(function(cell){
      var text = cell;
      if (!known(a.access, cell)) text += ' · нет в каталоге';
      select.appendChild(el('option', { value: cell, text: text }));
    });
    select.value = (tab !== 'default' && !row.overridden[role]) ? INHERIT : value;
    select.addEventListener('change', function(){
      var next = select.value;
      if (tab === 'default') {
        api({ op: 'SetCell', access: a.access, role: role, value: next });
      } else if (next === INHERIT) {
        api({ op: 'SetVariantCell', name: tab, access: a.access, role: role, inherit: true });
      } else {
        api({ op: 'SetVariantCell', name: tab, access: a.access, role: role, value: next });
      }
    });
    var wrap = el('div', { cls: 'rw-cell' }, [select]);
    if (value && !known(a.access, value)) wrap.appendChild(el('span', { cls: 'rw-cell-warn', title: 'модели нет в каталоге: docflow roster scan', text: '!' }));
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
        routes.push({ text: row.a.access + '/' + cell, missing: !known(row.a.access, cell) });
      });
      var line = el('div', { cls: 'rw-chain' }, [el('span', { cls: 'rw-chain-role', text: role })]);
      if (!routes.length) {
        line.appendChild(el('span', { cls: 'rw-chain-none', text: 'нет маршрута' }));
        warnings.push('нет маршрута для ' + role);
      }
      routes.forEach(function(r, i){
        if (i) line.appendChild(el('span', { cls: 'rw-chain-sep', text: '›' }));
        line.appendChild(el('span', { cls: 'rw-route' + (i === 0 ? ' is-primary' : '') + (r.missing ? ' is-missing' : ''), title: r.missing ? 'модели нет в каталоге: резолвер её пропустит' : '', text: r.text }));
      });
      if (routes.length === 1) warnings.push('нет fallback для ' + role);
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
          onclick: function(){ api({ op: 'AddAccess', name: c.access, kind: c.kind || '', agents: c.agents || [] }).then(function(ok){ if (ok) closeModal(); }); }
        }, [el('strong', { text: c.access }), el('span', { cls: 'rw-muted', text: ' ' + (c.agents || []).join(', ') + ' · ' + (c.kind || '—') + ' · моделей ' + c.models })]));
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
