/* docflow roster editor — the behavior of docs/design/roster-web-mockup.html
   driven by the local server: the state comes from GET /api/workspace,
   every action is POST /api/op with the operation names of the roster
   package, and the server's workspace in the response is the next render.
   No build step, no external dependencies. */
(function(){
  'use strict';
  var root = document.getElementById('rwe');
  if (!root) return;
  var $ = function(id){ return document.getElementById(id); };

  var ROLES = ['cheap','executor','heavy','review','acceptance','replan'];
  var JUDGES = { review:true, acceptance:true, replan:true };
  var CLASSES = ['cheap','standard','strong','trusted'];
  var BUDGETS = ['prefer','normal','lean','minimal','off'];
  var VBUDGETS = ['—','prefer','lean','minimal','off'];
  var RANK = { cheap:0, standard:1, strong:2 };
  var SRC_RU = { roster:'в roster.toml', variants:'глобальный файл вариантов', project:'проектный файл вариантов' };
  var SRC_SHORT = { roster:'политика', variants:'глобальный', project:'проектный' };
  var SRC_PATH = { roster:'~/.config/docflow/roster.toml', variants:'~/.config/docflow/roster.variants.toml', project:'docs/roster.variants.toml' };

  var ws = null;        // the workspace JSON of the last successful load
  var mode = 'base';    // 'base' or a variant name
  var serverError = ''; // the last refused operation, shown in the red line

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
        renderAll();
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

  function reloadWorkspace(){
    return fetch('api/workspace').then(function(resp){ return resp.json(); }).then(function(body){
      if (!body.ok || !body.workspace) throw new Error(body.error || 'HTTP ' + resp.status);
      ws = body.workspace;
      serverError = '';
      if (mode !== 'base' && !variant()) mode = 'base';
      renderAll();
      return true;
    }).catch(function(err){
      toast('ростер не прочитан: ' + String(err));
      return false;
    });
  }

  /* ---------- views of the workspace ---------- */

  // accesses returns the policy accesses as the editor models them: the
  // policy budget and the allowlist ids, the routes of the policy grouped
  // per model in file order, the effort "-" shown as no effort.
  function accesses(){
    var byAccess = {};
    ws.accesses.forEach(function(a){
      byAccess[a.access] = {
        id: a.access,
        agents: a.agents || [],
        kind: a.kind || 'subscription',
        trusted: !!a.trusted,
        check: !!a.check,
        budget: a.policy_budget || null,
        off: !!a.off,
        models: (a.policy_models || []).map(function(id){ return { id:id, routes:[] }; })
      };
    });
    ws.routes.forEach(function(r){
      var a = byAccess[r.access];
      if (!a) return;
      var m = null;
      a.models.forEach(function(c){ if (c.id === r.model) m = c; });
      if (!m) { m = { id:r.model, routes:[] }; a.models.push(m); }
      m.routes.push({ eff: r.eff === '-' ? null : r.eff, cls: r.class });
    });
    return ws.accesses.map(function(a){ return byAccess[a.access]; }).filter(Boolean);
  }

  function acc(id){
    var list = accesses();
    for (var i=0;i<list.length;i++) if (list[i].id===id) return list[i];
    return null;
  }

  function variant(){
    if (mode==='base') return null;
    for (var i=0;i<ws.variants.length;i++) if (ws.variants[i].name===mode) return rawVariant(ws.variants[i]);
    return null;
  }

  // rawVariant turns the preset lists of the workspace into the maps the
  // editor edits: budgets access → level, models access → [id].
  function rawVariant(v){
    var budgets = {};
    [['off', v.off], ['lean', v.lean], ['minimal', v.minimal], ['prefer', v.prefer]].forEach(function(pair){
      (pair[1] || []).forEach(function(a){ budgets[a] = pair[0]; });
    });
    return { name: v.name, source: v.source, budgets: budgets, models: v.models || {}, order: v.order || [] };
  }

  function catalogModel(aid, mid){
    var models = (ws.catalog_models || {})[aid] || [];
    for (var i=0;i<models.length;i++) if (models[i].id===mid) return models[i];
    return null;
  }
  function effortsOf(aid, mid){ var m = catalogModel(aid, mid); return m ? (m.efforts || []) : []; }

  function servedModelIds(a){
    if (a.models.length) return a.models.map(function(m){ return m.id; });
    return ((ws.catalog_models || {})[a.id] || []).map(function(m){ return m.id; });
  }

  function autoClass(id){
    if (/(^|[.\-_])(flash|mini|lite|haiku|nano)([.\-_]|$)/.test(id)) return 'cheap';
    if (/(pro|sol|opus|ultra)/.test(id)) return 'strong';
    return 'standard';
  }
  function fmtCtx(n){ return n >= 1000000 ? (Math.round(n/100000)/10).toString().replace('.0','') + 'M' : Math.round(n/1000)+'k'; }
  function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }
  function q(arr){ return '[' + arr.map(function(x){ return '"' + x + '"'; }).join(', ') + ']'; }
  function dotColor(aid){ return 'var(--rw-c-' + (aid==='opencode-go' ? 'og' : aid) + ', var(--rw-muted))'; }

  function roleBrief(role){
    for (var i=0;i<ws.roles.length;i++) if (ws.roles[i].role===role) return ws.roles[i];
    return { role: role, routes: [] };
  }

  /* ---------- TOML sketch (the file panel) ---------- */

  function rosterTOML(){
    var p = accesses(), L = ['version = 3', ''];
    p.forEach(function(a){
      L.push('[access.' + a.id + ']');
      L.push('agents = ' + q(a.agents));
      L.push('kind = "' + a.kind + '"');
      if (a.budget) L.push('budget = "' + a.budget + '"');
      if (a.trusted) L.push('trusted = true');
      if (a.check) L.push('check = true');
      if (a.models.length) L.push('models = ' + q(a.models.map(function(m){ return m.id; })));
      L.push('');
    });
    p.forEach(function(a){
      a.models.forEach(function(m){
        if (!m.routes.length) return;
        L.push('[route."' + a.id + '/' + m.id + '"]');
        m.routes.forEach(function(r){ L.push('"' + (r.eff === null ? '-' : r.eff) + '" = "' + r.cls + '"'); });
        L.push('');
      });
    });
    L.push('[role]');
    ROLES.forEach(function(r){ L.push(r + ' = "' + (ws.role_requirements[r] || 'standard') + '"'); });
    L.push('');
    ws.variants.filter(function(v){ return v.source==='roster'; }).forEach(function(v){ L = L.concat(presetLines(rawVariant(v))); });
    return L.join('\n').replace(/\n{3,}/g, '\n\n').trim();
  }
  function presetLines(v){
    var L = ['[preset.' + v.name + ']'];
    ['off','lean','minimal','prefer'].forEach(function(b){
      var ids = Object.keys(v.budgets).filter(function(a){ return v.budgets[a]===b; });
      if (ids.length) L.push(b + ' = ' + q(ids));
    });
    if (v.order.length) L.push('order = ' + q(v.order));
    var mk = Object.keys(v.models);
    if (mk.length) L.push('models = { ' + mk.map(function(a){ return a + ' = ' + q(v.models[a]); }).join(', ') + ' }');
    L.push('');
    return L;
  }
  function variantsTOML(source){
    var L = ['version = 2', ''];
    ws.variants.filter(function(v){ return v.source===source; }).forEach(function(v){ L = L.concat(presetLines(rawVariant(v))); });
    return L.join('\n').trim();
  }

  /* ---------- validation ---------- */

  function validations(){
    var out = [], p = accesses();
    p.forEach(function(a){
      if (a.budget === 'minimal' && !a.trusted)
        out.push('минимальный бюджет у ' + a.id + ' требует trusted: minimal обслуживает только судейские роли');
    });
    var v = variant();
    if (v) {
      if (!v.name.trim()) out.push('имя варианта пустое');
      if (ws.variants.some(function(x){ return x.name === v.name; }) && ws.variants.filter(function(x){ return x.name === v.name; }).length > 1)
        out.push('имя "' + v.name + '" уже занято — имена должны значить одно везде');
      Object.keys(v.budgets).forEach(function(aid){
        var a = acc(aid);
        if (v.budgets[aid] === 'minimal' && a && !a.trusted)
          out.push('вариант: minimal у ' + aid + ' требует trusted');
      });
    }
    if (p.length) {
      if (!p.some(function(a){ return !a.check; }))
        out.push('все доступы с check — ролям review/acceptance/replan не остаётся исполнителей');
      ['acceptance','replan'].forEach(function(r){
        if (ws.role_requirements[r] === 'trusted') {
          var judge = roleBrief(r);
          var strongTrusted = (judge.routes || []).some(function(x){ return x.class === 'strong'; });
          if (!strongTrusted) out.push('нет trusted-доступа с strong-маршрутом — ' + r + ' останется без исполнителя');
        }
      });
    }
    if (serverError) out.push('сервер: ' + serverError);
    return out;
  }

  /* ---------- rendering ---------- */

  function renderSwitch(){
    var h = ['<button type="button" class="rw-chip' + (mode==='base' ? ' is-active' : '') + '" data-mode="base">common</button>'];
    ws.variants.forEach(function(v){
      h.push('<button type="button" class="rw-chip is-variant' + (mode===v.name ? ' is-active' : '') + '" data-mode="' + esc(v.name) + '">' + esc(v.name) +
        ' <span class="rw-chip-src">· ' + (SRC_SHORT[v.source] || v.source) + '</span></button>');
    });
    h.push('<button type="button" class="rw-chip rw-chip-add" id="rwe-new-variant">+ вариант</button>');
    $('rwe-switch').innerHTML = h.join('');
  }

  function renderVal(){
    var v = validations();
    var el = $('rwe-val');
    el.className = 'rw-val' + (v.length ? ' has' : '');
    el.innerHTML = v.map(function(s){ return '<div>⚠ ' + esc(s) + '</div>'; }).join('');
    var problem = $('rwe-problem');
    if (ws.problem || ws.missing || ws.legacy || ws.catalog_empty) {
      problem.hidden = false;
      problem.textContent = ws.problem || (ws.missing ? 'ростер не найден — создайте политику' :
        ws.legacy ? 'ростер старого формата — выполните docflow roster migrate' :
        'каталог пуст — выполните docflow roster scan');
    } else {
      problem.hidden = true;
    }
  }

  function renderBase(){
    var p = accesses();
    $('rwe-cards').innerHTML = p.map(function(a){
      var seg = BUDGETS.map(function(b){
        var on = (a.budget || 'normal') === b;
        return '<button type="button" data-seg="' + esc(a.id) + '" data-b="' + b + '"' + (on ? ' class="is-on"' : '') + ' title="Базовый бюджет доступа' + (b==='normal' ? ' (по умолчанию)' : '') + '">' + b + '</button>';
      }).join('');
      var flagTr = '<button type="button" class="rw-flag is-trusted' + (a.trusted ? ' is-on' : '') + '" data-flag="trusted" data-a="' + esc(a.id) + '" title="Допускается к судейским ролям (review, acceptance, replan)">trusted</button>';
      var flagCh = '<button type="button" class="rw-flag is-check' + (a.check ? ' is-on' : '') + '" data-flag="check" data-a="' + esc(a.id) + '" title="Сам требует независимого ревью — не судит">check</button>';
      var modelsHtml;
      if (a.models.length) {
        modelsHtml = a.models.map(function(m){
          var cat = catalogModel(a.id, m.id);
          var effs = effortsOf(a.id, m.id);
          var routes = m.routes.map(function(r, ri){
            var effSel;
            if (effs.length) {
              effSel = '<select class="rw-sel" data-reff="' + esc(a.id) + '"' + dataAttrM(m.id) + ' data-re="' + esc(r.eff === null ? '-' : r.eff) + '" aria-label="Ризонинг">' +
                effs.map(function(e){ return '<option value="' + e + '"' + (r.eff===e ? ' selected' : '') + '>' + e + '</option>'; }).join('') + '</select>';
            } else {
              effSel = '<span class="rw-arrow">без усилия</span>';
            }
            var clsSel = '<select class="rw-sel cls-' + r.cls + '" data-rcls="' + esc(a.id) + '"' + dataAttrM(m.id) + ' data-re="' + esc(r.eff === null ? '-' : r.eff) + '" aria-label="Класс">' +
              ['cheap','standard','strong'].map(function(c){ return '<option value="' + c + '"' + (r.cls===c ? ' selected' : '') + '>' + c + '</option>'; }).join('') + '</select>';
            return '<div class="rw-route2">' + effSel + '<span class="rw-arrow">→</span>' + clsSel +
              '<button type="button" class="rw-rx" data-rdel="' + esc(a.id) + '"' + dataAttrM(m.id) + ' data-re="' + esc(r.eff === null ? '-' : r.eff) + '" aria-label="Убрать уровень">×</button></div>';
          }).join('');
          var unused = effs.filter(function(e){ return !m.routes.some(function(r){ return r.eff === e; }); });
          var addBtn = effs.length
            ? '<button type="button" class="rw-addroute" data-radd="' + esc(a.id) + '" data-m="' + esc(m.id) + '"' + (unused.length ? '' : ' disabled') + ' title="' + (unused.length ? 'Добавить уровень ' + unused.join(', ') : 'Все уровни уже добавлены') + '">+ уровень' + (unused.length === 1 ? ' ' + unused[0] : '') + '</button>'
            : '';
          var catInfo = cat ? fmtCtx(cat.context || 0) + ' · $' + (cat.input || 0) + '/' + (cat.output || 0) : '';
          return '<div class="rw-model"><div class="rw-model-head"><span class="rw-mname" title="' + esc(catInfo) + '">' + esc(m.id) + '</span>' +
            '<button type="button" class="rw-rx" data-mdel="' + esc(a.id) + '" data-m="' + esc(m.id) + '" aria-label="Убрать модель">×</button></div>' +
            routes + addBtn + '</div>';
        }).join('');
      } else {
        modelsHtml = '<div class="rw-mall">моделей нет — ростер берёт все модели доступа (' + ((ws.catalog_models || {})[a.id] || []).length + ')</div>';
      }
      return '<div class="rw-card' + ((a.budget||'normal')==='off' || a.off ? ' is-off' : '') + '">' +
        '<div class="rw-card-head"><span class="rw-adot" style="background:' + dotColor(a.id) + '"></span>' +
          '<span class="rw-aname">' + esc(a.id) + '</span>' +
          '<span class="rw-tag">' + esc(a.kind) + '</span>' +
          '<span class="rw-via">через ' + esc(a.agents.join(', ')) + '</span>' +
          '<span class="rw-gap"></span>' + flagTr + flagCh +
          '<button type="button" class="rw-rx" data-adel="' + esc(a.id) + '" aria-label="Удалить доступ" title="Удалить доступ из ростера">×</button></div>' +
        '<div class="rw-seg" role="group" aria-label="Бюджет ' + esc(a.id) + '">' + seg + '</div>' +
        '<div style="display:flex;flex-direction:column;gap:6px">' + modelsHtml + '</div>' +
        '<button type="button" class="rw-addm" data-mpick="' + esc(a.id) + '">+ модель из каталога</button>' +
      '</div>';
    }).join('');

    var left = (ws.catalog_accesses || []).map(function(a){ return a.access; }).filter(function(id){ return !acc(id); });
    $('rwe-catalog-left').textContent = left.length ? 'в каталоге есть: ' + left.join(', ') : 'все доступы каталога уже в ростере';

    $('rwe-matrix').innerHTML = ROLES.map(function(r){
      var req = ws.role_requirements[r] || 'standard';
      var brief = roleBrief(r);
      var routes = brief.routes || [];
      function items(list){
        return list.map(function(x){
          return '<span class="rw-rri" title="' + esc(x.class || '') + '"><span class="rw-adot" style="background:' + dotColor(x.access) + '"></span>' +
            esc(x.access + '/' + x.id + (x.effort ? '@' + x.effort : '')) + '</span>';
        }).join('');
      }
      var body = items(routes);
      if (!routes.length) body = '<span class="rw-rnone">нет маршрутов</span>';
      if (JUDGES[r]) body += ' <span class="rw-rnote">check-доступы не судят</span>';
      return '<div class="rw-role2">' +
        '<span class="rw-rname">' + r + '</span>' +
        '<select class="rw-sel rw-rreq" data-rolesel="' + r + '" aria-label="Требование роли ' + r + '">' +
          CLASSES.map(function(c){ return '<option value="' + c + '"' + (req===c ? ' selected' : '') + '>' + c + '</option>'; }).join('') + '</select>' +
        '<div class="rw-rroutes">' + body + '</div></div>';
    }).join('');
  }
  function dataAttrM(mid){ return ' data-m="' + esc(mid) + '"'; }

  function sortedAccesses(v){
    var rest = accesses().map(function(a){ return a.id; }).filter(function(id){ return v.order.indexOf(id) < 0; });
    return v.order.filter(function(id){ return acc(id); }).concat(rest);
  }

  function renderVariant(){
    var v = variant();
    if (!v) { setMode('base'); return; }
    var input = $('rwe-vname-input');
    input.value = v.name;
    input.className = 'rw-vname' + (!v.name.trim() ? ' is-bad' : '');
    $('rwe-vsrc').textContent = SRC_RU[v.source] || v.source;

    var ids = sortedAccesses(v);
    $('rwe-vrows').innerHTML = ids.map(function(id, i){
      var a = acc(id);
      if (!a) return '';
      var seg = VBUDGETS.map(function(b){
        var on = b === '—' ? !v.budgets[id] : v.budgets[id] === b;
        return '<button type="button" data-vseg="' + esc(id) + '" data-b="' + b + '"' + (on ? ' class="is-on"' : '') + ' title="' + (b==='—' ? 'Наследует базовый ростер' : 'Отклонение от базового бюджета') + '">' + b + '</button>';
      }).join('');
      var chips = a.models.length ? servedModelIds(a).map(function(mid){
        var on = !v.models[id] || v.models[id].indexOf(mid) >= 0;
        return '<button type="button" class="rw-vchip' + (on ? ' is-on' : '') + '" data-vmodel="' + esc(id) + '" data-m="' + esc(mid) + '" title="Сужение моделей варианта (переключить)">' + esc(mid) + '</button>';
      }).join('') : '<span class="rw-rnote">все модели доступа</span>';
      return '<div class="rw-vrow">' +
        '<div class="rw-ord">' +
          '<button type="button" data-vmove="up" data-acc="' + esc(id) + '" aria-label="Выше"' + (i===0 ? ' disabled' : '') + '>▲</button>' +
          '<button type="button" data-vmove="down" data-acc="' + esc(id) + '" aria-label="Ниже"' + (i===ids.length-1 ? ' disabled' : '') + '>▼</button></div>' +
        '<span class="rw-adot" style="background:' + dotColor(id) + '"></span><span class="rw-aname">' + esc(id) + '</span>' +
        '<span class="rw-via">' + (v.budgets[id] ? '' : 'базовый бюджет · ') + 'через ' + esc(a.agents.join(', ')) + '</span>' +
        '<div class="rw-vseg" role="group" aria-label="Бюджет ' + esc(id) + ' в варианте">' + seg + '</div>' +
        '<div class="rw-vmodels">' + chips + '</div>' +
      '</div>';
    }).join('') || '<div class="rw-empty">в ростере нет доступов</div>';
  }

  function renderSheet(){
    var v = variant();
    var path, text;
    if (!v) { path = '~/.config/docflow/roster.toml — базовый ростер (common)'; text = rosterTOML(); }
    else if (v.source === 'roster') { path = SRC_PATH.roster + ' — пресет живёт в базовом файле'; text = rosterTOML(); }
    else { path = SRC_PATH[v.source]; text = variantsTOML(v.source); }
    $('rwe-sheet-path').textContent = path;
    $('rwe-toml').textContent = text + '\n';
  }

  function setMode(m){
    mode = m;
    $('rwe-base').hidden = mode !== 'base';
    $('rwe-variant').hidden = mode === 'base';
    renderSwitch(); renderVal();
    if (mode === 'base') renderBase(); else renderVariant();
    renderSheet();
  }

  function renderAll(){
    if (!ws) return;
    setMode(mode);
  }

  /* ---------- toasts and modals ---------- */

  function toast(msg){
    var t = document.createElement('div');
    t.className = 'rw-toast';
    t.textContent = msg;
    $('rwe-toasts').appendChild(t);
    setTimeout(function(){ t.remove(); }, 4200);
  }
  function openModal(html){ $('rwe-modal').innerHTML = html; $('rwe-overlay').classList.add('open'); }
  function closeModal(){ $('rwe-overlay').classList.remove('open'); $('rwe-modal').innerHTML = ''; }

  function openModelPicker(aid){
    var a = acc(aid);
    var models = (ws.catalog_models || {})[aid] || [];
    openModal(
      '<div class="rw-modal-head"><h3>Модели доступа <code>' + esc(aid) + '</code></h3><button type="button" class="rw-x" data-close aria-label="Закрыть">×</button></div>' +
      '<input type="search" class="rw-search" id="rwe-msearch" placeholder="Поиск модели…" aria-label="Поиск модели">' +
      '<div class="rw-list" id="rwe-mlist"></div>' +
      '<div class="rw-modal-foot"><span class="rw-count" id="rwe-mcount"></span>' +
      '<button type="button" class="rw-btn" data-close>Отмена</button>' +
      '<button type="button" class="rw-btn rw-btn-primary" id="rwe-madd" disabled>Добавить</button></div>' +
      '<p class="rw-hint" style="margin:10px 0 0">Список и ризонинги — из каталога <code>docflow roster scan</code>. Новая модель получает первый уровень ризонинга и класс по имени — поправьте на карточке.</p>'
    );
    var picked = {};
    function draw(qs){
      qs = (qs||'').toLowerCase();
      var inPolicy = {};
      a.models.forEach(function(m){ inPolicy[m.id] = true; });
      var rows = models.filter(function(m){ return m.id.toLowerCase().indexOf(qs) >= 0; });
      $('rwe-mlist').innerHTML = rows.map(function(m){
        var effs = m.efforts || [];
        return '<label class="rw-lrow"' + (inPolicy[m.id] ? ' style="opacity:.5"' : '') + '>' +
          '<input type="checkbox" data-pick="' + esc(m.id) + '"' + (inPolicy[m.id] ? ' disabled' : '') + (picked[m.id] ? ' checked' : '') + '>' +
          '<span class="rw-mid">' + esc(m.id) + '</span>' +
          '<span class="rw-meta2">' + (effs.length ? esc(effs.join(',')) + ' · ' : '') + fmtCtx(m.context || 0) + ' · $' + (m.input || 0) + '/' + (m.output || 0) + (inPolicy[m.id] ? ' · уже в ростере' : ' · авто: ' + autoClass(m.id)) + '</span></label>';
      }).join('') || '<div class="rw-empty">ничего не найдено</div>';
      recount();
    }
    function recount(){
      var n = Object.keys(picked).filter(function(k){ return picked[k]; }).length;
      var btn = $('rwe-madd');
      btn.disabled = !n;
      btn.textContent = 'Добавить' + (n ? ' (' + n + ')' : '');
    }
    $('rwe-mlist').addEventListener('change', function(e){
      var cb = e.target;
      if (cb.hasAttribute && cb.hasAttribute('data-pick')) { picked[cb.getAttribute('data-pick')] = cb.checked; recount(); }
    });
    $('rwe-madd').addEventListener('click', function(){
      var ids = Object.keys(picked).filter(function(id){ return picked[id]; });
      var chain = Promise.resolve(true);
      ids.forEach(function(id){ chain = chain.then(function(){ return api({ op:'AddAccessModel', access: aid, id: id }); }); });
      chain.then(function(ok){ if (ok) { closeModal(); toast('Модели добавлены в ' + aid); } });
    });
    draw('');
    $('rwe-msearch').addEventListener('input', function(){ draw(this.value); });
  }

  function openAccessPicker(){
    var avail = (ws.catalog_accesses || []).map(function(a){ return a; }).filter(function(a){ return !acc(a.access); });
    openModal(
      '<div class="rw-modal-head"><h3>Добавить доступ</h3><button type="button" class="rw-x" data-close aria-label="Закрыть">×</button></div>' +
      '<div class="rw-list">' + (avail.length ? avail.map(function(a){
        return '<button type="button" class="rw-lrow" data-aadd="' + esc(a.access) + '">' +
          '<span class="rw-mid"><span class="rw-adot" style="background:' + dotColor(a.access) + ';display:inline-block;margin-right:6px"></span>' + esc(a.access) + '</span>' +
          '<span class="rw-meta2">через ' + esc((a.agents || []).join(', ')) + ' · ' + a.models + ' моделей</span></button>';
      }).join('') : '<div class="rw-empty">в каталоге не осталось доступов вне ростера</div>') + '</div>' +
      '<p class="rw-hint">Новый доступ появляется без моделей — добавьте нужные из каталога.</p>'
    );
  }

  function openNewVariant(){
    var machine = ws.variants.filter(function(v){ return v.source !== 'roster'; });
    var copyOpts = ['<option value="">пустой</option>'].concat(machine.map(function(v){
      return '<option value="' + esc(v.name) + '">копия «' + esc(v.name) + '»</option>';
    })).join('');
    openModal(
      '<div class="rw-modal-head"><h3>Новый вариант ростера</h3><button type="button" class="rw-x" data-close aria-label="Закрыть">×</button></div>' +
      '<div class="rw-field"><span>Имя</span><input class="rw-input" id="rwe-nv-name" placeholder="например, lean-week"></div>' +
      '<div class="rw-field"><span>Файл</span><select class="rw-select2" id="rwe-nv-layer">' +
        '<option value="variants">глобальный — ~/.config/docflow/roster.variants.toml</option>' +
        '<option value="project">проектный — docs/roster.variants.toml (коммитится)</option></select></div>' +
      '<div class="rw-field"><span>Начать с</span><select class="rw-select2" id="rwe-nv-copy">' + copyOpts + '</select></div>' +
      '<div class="rw-modal-foot"><button type="button" class="rw-btn" data-close>Отмена</button>' +
      '<button type="button" class="rw-btn rw-btn-primary" id="rwe-nv-create">Создать</button></div>'
    );
    $('rwe-nv-create').addEventListener('click', function(){
      var name = $('rwe-nv-name').value.trim();
      if (!name) { toast('Имя пустое'); return; }
      api({ op:'CreateVariant', name: name, project: $('rwe-nv-layer').value === 'project', copy_of: $('rwe-nv-copy').value })
        .then(function(ok){ if (ok) { closeModal(); mode = name; renderAll(); toast('Вариант "' + name + '" создан'); } });
    });
  }

  /* ---------- events ---------- */

  root.addEventListener('click', function(e){
    var t = e.target.closest('button');
    if (!t) return;

    if (t.hasAttribute('data-mode')) { setMode(t.getAttribute('data-mode')); return; }
    if (t.id === 'rwe-new-variant') { openNewVariant(); return; }
    if (t.hasAttribute('data-close')) { closeModal(); return; }
    if (t.id === 'rwe-file') { $('rwe-sheet').classList.add('open'); return; }
    if (t.id === 'rwe-close-sheet') { $('rwe-sheet').classList.remove('open'); return; }
    if (t.id === 'rwe-reload') { reloadWorkspace().then(function(ok){ if (ok) toast('Ростер перечитан'); }); return; }
    if (t.id === 'rwe-add-access') { openAccessPicker(); return; }
    if (t.hasAttribute('data-aadd')) {
      var nid = t.getAttribute('data-aadd');
      var agents = [];
      (ws.catalog_accesses || []).forEach(function(a){ if (a.access === nid) agents = a.agents || []; });
      api({ op:'AddAccess', name: nid, kind:'subscription', agents: agents })
        .then(function(ok){ if (ok) { closeModal(); toast('Доступ ' + nid + ' добавлен'); } });
      return;
    }
    if (t.hasAttribute('data-adel')) {
      var did = t.getAttribute('data-adel');
      api({ op:'RemoveAccess', name: did }).then(function(ok){ if (ok) toast('Доступ ' + did + ' удалён из ростера'); });
      return;
    }
    if (t.hasAttribute('data-mpick')) { openModelPicker(t.getAttribute('data-mpick')); return; }
    if (t.hasAttribute('data-mdel')) {
      api({ op:'RemoveAccessModel', access: t.getAttribute('data-mdel'), id: t.getAttribute('data-m') });
      return;
    }
    if (t.hasAttribute('data-radd')) {
      api({ op:'AddRouteLevel', access: t.getAttribute('data-radd'), model: t.getAttribute('data-m') });
      return;
    }
    if (t.hasAttribute('data-rdel')) {
      api({ op:'RemoveRoute', access: t.getAttribute('data-rdel'), model: t.getAttribute('data-m'), effort: t.getAttribute('data-re') });
      return;
    }
    if (t.hasAttribute('data-seg')) {
      var a1 = acc(t.getAttribute('data-seg')), b1 = t.getAttribute('data-b');
      var next1 = (a1.budget || 'normal') === b1 ? '' : b1;
      api({ op:'SetAccessBudget', name: a1.id, level: next1 });
      return;
    }
    if (t.hasAttribute('data-flag')) {
      var a2 = acc(t.getAttribute('data-a')), f = t.getAttribute('data-flag');
      var trusted = f === 'trusted' ? !a2.trusted : false;
      var check = f === 'check' ? !a2.check : false;
      api({ op:'SetAccessFlags', name: a2.id, trusted: trusted, check: check });
      return;
    }
    if (t.hasAttribute('data-vseg')) {
      var v = variant(); if (!v) return;
      var va = t.getAttribute('data-vseg'), vb = t.getAttribute('data-b');
      var lvl = vb === '—' ? '' : vb;
      if (lvl && v.budgets[va] === lvl) lvl = '';
      api({ op:'SetVariantBudget', name: mode, access: va, level: lvl });
      return;
    }
    if (t.hasAttribute('data-vmodel')) {
      api({ op:'ToggleVariantModel', name: mode, access: t.getAttribute('data-vmodel'), id: t.getAttribute('data-m') });
      return;
    }
    if (t.hasAttribute('data-vmove')) {
      api({ op:'MoveVariantAccess', name: mode, access: t.getAttribute('data-acc'), dir: t.getAttribute('data-vmove') });
      return;
    }
    if (t.id === 'rwe-vdelete') {
      var deleted = mode;
      api({ op:'DeleteVariant', name: mode }).then(function(ok){
        if (ok) { mode = 'base'; renderAll(); toast('Вариант "' + deleted + '" удалён'); }
      });
      return;
    }
  });

  root.addEventListener('change', function(e){
    var el = e.target;
    if (!el.hasAttribute) return;
    if (el.hasAttribute('data-rcls')) {
      api({ op:'SetRouteClass', access: el.getAttribute('data-rcls'), model: el.getAttribute('data-m'), effort: el.getAttribute('data-re'), 'class': el.value });
      return;
    }
    if (el.hasAttribute('data-reff')) {
      api({ op:'SetRouteEffort', access: el.getAttribute('data-reff'), model: el.getAttribute('data-m'), effort: el.getAttribute('data-re'), next: el.value });
      return;
    }
    if (el.hasAttribute('data-rolesel')) {
      api({ op:'SetRole', role: el.getAttribute('data-rolesel'), requirement: el.value });
      return;
    }
    if (el.id === 'rwe-vname-input') {
      var name = el.value.trim();
      if (!name || name === mode) { renderVariant(); return; }
      api({ op:'RenameVariant', name: mode, next: name }).then(function(ok){ if (ok) { mode = name; renderAll(); } });
    }
  });

  $('rwe-overlay').addEventListener('click', function(e){ if (e.target === this) closeModal(); });

  reloadWorkspace();
})();
