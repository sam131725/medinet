package webui

// customerPageHTML is a single, self-contained page (no external requests,
// no CDN scripts) so it keeps working with zero internet access. It talks
// only to this same server's /api/* endpoints on the local machine/network.
const customerPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, user-scalable=no">
<title>MediStock - Order Medicine</title>
<style>
  :root {
    --primary: #1565c0;
    --primary-dark: #0d47a1;
    --danger: #c62828;
    --success: #2e7d32;
    --bg: #f4f6f8;
    --card: #ffffff;
    --border: #dde3e8;
    --text: #1a1f24;
    --muted: #5c6b77;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; font-family: -apple-system, "Segoe UI", Roboto, Arial, sans-serif;
    background: var(--bg); color: var(--text); padding-bottom: 140px;
  }
  header {
    background: var(--primary); color: #fff; padding: 20px 16px; text-align: center;
    position: sticky; top: 0; z-index: 10;
  }
  header h1 { margin: 0; font-size: 22px; }
  header p { margin: 4px 0 0; font-size: 14px; opacity: 0.9; }
  .search-bar { padding: 14px 16px; background: var(--card); border-bottom: 1px solid var(--border); }
  .search-bar input {
    width: 100%; padding: 14px 16px; font-size: 17px; border-radius: 10px;
    border: 2px solid var(--border); outline: none;
  }
  .search-bar input:focus { border-color: var(--primary); }
  #grid { padding: 12px; display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; }
  .med-card {
    background: var(--card); border: 1px solid var(--border); border-radius: 14px;
    padding: 14px; display: flex; flex-direction: column; gap: 8px; min-height: 120px;
  }
  .med-name { font-weight: 700; font-size: 16px; line-height: 1.25; }
  .med-meta { font-size: 13px; color: var(--muted); }
  .med-price { font-size: 18px; font-weight: 700; color: var(--primary-dark); }
  .qty-row { display: flex; align-items: center; gap: 8px; margin-top: auto; }
  .qty-btn {
    width: 40px; height: 40px; border-radius: 10px; border: none; background: var(--primary);
    color: #fff; font-size: 20px; font-weight: bold; cursor: pointer;
  }
  .qty-btn:active { background: var(--primary-dark); }
  .qty-btn:disabled { background: #b7c3cc; cursor: not-allowed; }
  .qty-val { flex: 1; text-align: center; font-size: 18px; font-weight: 700; }
  .empty-msg, .loading { text-align: center; color: var(--muted); padding: 40px 16px; }
  #cart-bar {
    position: fixed; bottom: 0; left: 0; right: 0; background: var(--card);
    border-top: 2px solid var(--border); padding: 12px 16px; box-shadow: 0 -4px 12px rgba(0,0,0,0.08);
  }
  #cart-summary { display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; font-size: 16px; }
  #cart-total { font-weight: 800; font-size: 20px; color: var(--primary-dark); }
  #checkout-btn {
    width: 100%; padding: 16px; font-size: 18px; font-weight: 700; border: none;
    border-radius: 12px; background: var(--success); color: #fff; cursor: pointer;
  }
  #checkout-btn:disabled { background: #a5c6a8; cursor: not-allowed; }
  .overlay {
    position: fixed; inset: 0; background: rgba(0,0,0,0.5); display: none;
    align-items: center; justify-content: center; padding: 16px; z-index: 50;
  }
  .overlay.show { display: flex; }
  .modal { background: #fff; border-radius: 16px; padding: 24px; max-width: 420px; width: 100%; max-height: 85vh; overflow-y: auto; }
  .modal h2 { margin-top: 0; }
  .modal label { display: block; font-size: 14px; color: var(--muted); margin-top: 12px; margin-bottom: 4px; }
  .modal input { width: 100%; padding: 12px; font-size: 16px; border-radius: 8px; border: 1px solid var(--border); }
  .modal .row { display: flex; justify-content: space-between; padding: 6px 0; font-size: 15px; }
  .modal .actions { display: flex; gap: 10px; margin-top: 20px; }
  .modal .actions button {
    flex: 1; padding: 14px; border-radius: 10px; border: none; font-size: 16px; font-weight: 700; cursor: pointer;
  }
  .btn-cancel { background: #eceff1; color: var(--text); }
  .btn-confirm { background: var(--success); color: #fff; }
  .token-box {
    text-align: center; background: #e8f5e9; border: 2px dashed var(--success); border-radius: 12px;
    padding: 20px; margin: 12px 0;
  }
  .token-box .token { font-size: 32px; font-weight: 800; color: var(--success); }
  .error-box { background: #fdecea; color: var(--danger); border-radius: 10px; padding: 12px; margin-top: 12px; font-size: 14px; }
  .cap-note { font-size: 12px; color: var(--danger); font-weight: 600; }
</style>
</head>
<body>

<header>
  <h1>MediStock Kiosk</h1>
  <p>Offline emergency medicine ordering - no internet needed</p>
</header>

<div class="search-bar">
  <input id="search" type="text" placeholder="Search medicine name..." autocomplete="off">
</div>

<div id="grid" class="loading">Loading medicines...</div>

<div id="cart-bar">
  <div id="cart-summary">
    <span id="cart-count">0 items</span>
    <span id="cart-total">Total: 0.00</span>
  </div>
  <button id="checkout-btn" disabled>Review &amp; Checkout</button>
</div>

<div id="checkout-overlay" class="overlay">
  <div class="modal">
    <h2>Confirm your order</h2>
    <div id="checkout-items"></div>
    <label>Your name (optional)</label>
    <input id="name-input" type="text" placeholder="Leave blank to stay anonymous">
    <label>Phone number (optional)</label>
    <input id="phone-input" type="tel" placeholder="Optional">
    <div id="checkout-error"></div>
    <div class="actions">
      <button class="btn-cancel" onclick="closeCheckout()">Cancel</button>
      <button class="btn-confirm" onclick="submitOrder()">Confirm order</button>
    </div>
  </div>
</div>

<div id="token-overlay" class="overlay">
  <div class="modal">
    <h2>Order placed!</h2>
    <div class="token-box">
      <div>Your token number</div>
      <div class="token" id="token-value">#0</div>
    </div>
    <p>Show this token number at the counter to collect your medicine.</p>
    <div id="token-items"></div>
    <div class="actions">
      <button class="btn-confirm" style="flex:1" onclick="startOver()">Done - New Order</button>
    </div>
  </div>
</div>

<script>
let medicines = [];
let cart = {}; // medicineId -> quantity

async function loadMedicines(query) {
  const grid = document.getElementById('grid');
  grid.className = 'loading';
  grid.textContent = 'Loading medicines...';
  try {
    const res = await fetch('/api/medicines' + (query ? ('?q=' + encodeURIComponent(query)) : ''));
    medicines = await res.json();
    renderGrid();
  } catch (e) {
    grid.className = 'empty-msg';
    grid.textContent = 'Could not load medicines. Is the server running?';
  }
}

function limitFor(m) {
  if (m.MaxPerOrder > 0 && m.MaxPerOrder < m.Quantity) return m.MaxPerOrder;
  return m.Quantity;
}

function renderGrid() {
  const grid = document.getElementById('grid');
  if (!medicines || medicines.length === 0) {
    grid.className = 'empty-msg';
    grid.textContent = 'No medicines available right now.';
    return;
  }
  grid.className = '';
  grid.innerHTML = '';
  medicines.forEach(m => {
    const limit = limitFor(m);
    const qty = cart[m.ID] || 0;
    const card = document.createElement('div');
    card.className = 'med-card';
    card.innerHTML =
      '<div class="med-name"></div>' +
      '<div class="med-meta"></div>' +
      '<div class="med-price"></div>' +
      (m.MaxPerOrder > 0 ? '<div class="cap-note">Max ' + m.MaxPerOrder + ' per order</div>' : '') +
      '<div class="qty-row">' +
        '<button class="qty-btn minus">-</button>' +
        '<div class="qty-val">' + qty + '</div>' +
        '<button class="qty-btn plus">+</button>' +
      '</div>';
    card.querySelector('.med-name').textContent = m.Name;
    card.querySelector('.med-meta').textContent = m.Manufacturer + ' | In stock: ' + m.Quantity + ' | SMS code: ' + m.Code;
    card.querySelector('.med-price').textContent = m.Price.toFixed(2) + ' / unit';

    const minusBtn = card.querySelector('.minus');
    const plusBtn = card.querySelector('.plus');
    minusBtn.disabled = qty <= 0;
    plusBtn.disabled = qty >= limit;

    minusBtn.onclick = () => { changeQty(m.ID, -1); };
    plusBtn.onclick = () => { changeQty(m.ID, 1); };

    grid.appendChild(card);
  });
}

function changeQty(id, delta) {
  const m = medicines.find(x => x.ID === id);
  if (!m) return;
  const limit = limitFor(m);
  let qty = (cart[id] || 0) + delta;
  if (qty < 0) qty = 0;
  if (qty > limit) qty = limit;
  if (qty === 0) delete cart[id]; else cart[id] = qty;
  renderGrid();
  updateCartBar();
}

function updateCartBar() {
  let count = 0, total = 0;
  Object.keys(cart).forEach(id => {
    const m = medicines.find(x => x.ID === Number(id));
    if (!m) return;
    count += cart[id];
    total += cart[id] * m.Price;
  });
  document.getElementById('cart-count').textContent = count + ' item' + (count === 1 ? '' : 's');
  document.getElementById('cart-total').textContent = 'Total: ' + total.toFixed(2);
  document.getElementById('checkout-btn').disabled = count === 0;
}

document.getElementById('search').addEventListener('input', (e) => {
  clearTimeout(window._searchTimer);
  window._searchTimer = setTimeout(() => loadMedicines(e.target.value), 250);
});

document.getElementById('checkout-btn').addEventListener('click', () => {
  const wrap = document.getElementById('checkout-items');
  wrap.innerHTML = '';
  Object.keys(cart).forEach(id => {
    const m = medicines.find(x => x.ID === Number(id));
    if (!m) return;
    const row = document.createElement('div');
    row.className = 'row';
    row.innerHTML = '<span></span><span></span>';
    row.children[0].textContent = m.Name + ' x' + cart[id];
    row.children[1].textContent = (m.Price * cart[id]).toFixed(2);
    wrap.appendChild(row);
  });
  document.getElementById('checkout-error').innerHTML = '';
  document.getElementById('checkout-overlay').classList.add('show');
});

function closeCheckout() {
  document.getElementById('checkout-overlay').classList.remove('show');
}

async function submitOrder() {
  const items = Object.keys(cart).map(id => ({ medicineId: Number(id), quantity: cart[id] }));
  const body = {
    name: document.getElementById('name-input').value.trim(),
    phone: document.getElementById('phone-input').value.trim(),
    items: items,
  };
  const errBox = document.getElementById('checkout-error');
  errBox.innerHTML = '';
  try {
    const res = await fetch('/api/checkout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const data = await res.json();
    if (!res.ok) {
      errBox.innerHTML = '<div class="error-box">' + (data.error || 'Order failed') + '</div>';
      return;
    }
    showToken(data);
  } catch (e) {
    errBox.innerHTML = '<div class="error-box">Could not reach the server. Please try again.</div>';
  }
}

function showToken(order) {
  closeCheckout();
  document.getElementById('token-value').textContent = '#' + order.ID;
  const wrap = document.getElementById('token-items');
  wrap.innerHTML = '';
  (order.Items || []).forEach(it => {
    const row = document.createElement('div');
    row.className = 'row';
    row.innerHTML = '<span></span><span></span>';
    row.children[0].textContent = it.MedicineName + ' x' + it.Quantity;
    row.children[1].textContent = it.Subtotal.toFixed(2);
    wrap.appendChild(row);
  });
  const totalRow = document.createElement('div');
  totalRow.className = 'row';
  totalRow.style.fontWeight = '700';
  totalRow.innerHTML = '<span>Total</span><span></span>';
  totalRow.children[1].textContent = order.Total.toFixed(2);
  wrap.appendChild(totalRow);
  document.getElementById('token-overlay').classList.add('show');
}

function startOver() {
  cart = {};
  document.getElementById('token-overlay').classList.remove('show');
  document.getElementById('name-input').value = '';
  document.getElementById('phone-input').value = '';
  loadMedicines('');
  updateCartBar();
}

loadMedicines('');
</script>
</body>
</html>`

// staffPageHTML is the PIN-gated staff/inventory management page.
const staffPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>MediStock - Staff</title>
<style>
  :root { --primary: #1565c0; --bg: #f4f6f8; --border: #dde3e8; --text: #1a1f24; --muted: #5c6b77; --danger:#c62828; --success:#2e7d32; }
  * { box-sizing: border-box; }
  body { margin: 0; font-family: -apple-system, "Segoe UI", Roboto, Arial, sans-serif; background: var(--bg); color: var(--text); }
  header { background: var(--primary); color: #fff; padding: 18px 16px; }
  header h1 { margin: 0; font-size: 20px; }
  .container { max-width: 900px; margin: 0 auto; padding: 16px; }
  .card { background: #fff; border: 1px solid var(--border); border-radius: 12px; padding: 16px; margin-bottom: 16px; }
  .card h2 { margin-top: 0; font-size: 17px; }
  table { width: 100%; border-collapse: collapse; font-size: 14px; }
  th, td { text-align: left; padding: 8px 6px; border-bottom: 1px solid var(--border); }
  th { color: var(--muted); font-weight: 600; }
  .low { color: var(--danger); font-weight: 700; }
  .form-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px,1fr)); gap: 10px; }
  input { width: 100%; padding: 10px; border-radius: 8px; border: 1px solid var(--border); font-size: 14px; }
  button { padding: 10px 16px; border: none; border-radius: 8px; background: var(--primary); color: #fff; font-weight: 700; cursor: pointer; margin-top: 10px; }
  .pin-gate { max-width: 320px; margin: 60px auto; text-align: center; }
  .msg { font-size: 13px; margin-top: 8px; }
  .msg.error { color: var(--danger); }
  .msg.ok { color: var(--success); }
  .stock-btns { display: flex; gap: 6px; }
  .stock-btns button { margin-top: 0; padding: 6px 10px; }
</style>
</head>
<body>

<div id="pin-gate" class="pin-gate">
  <h2>Staff Access</h2>
  <input id="pin-input" type="password" placeholder="Enter staff PIN" inputmode="numeric">
  <button onclick="unlock()">Unlock</button>
  <div id="pin-msg" class="msg"></div>
</div>

<div id="staff-area" style="display:none;">
  <header><h1>MediStock - Staff</h1></header>
  <div class="container">

    <div class="card">
      <h2>Add medicine</h2>
      <div class="form-grid">
        <input id="f-name" placeholder="Name">
        <input id="f-code" placeholder="SMS code (blank=auto)">
        <input id="f-manufacturer" placeholder="Manufacturer">
        <input id="f-batch" placeholder="Batch number">
        <input id="f-expiry" placeholder="Expiry (YYYY-MM-DD)">
        <input id="f-price" type="number" step="0.01" placeholder="Price per unit">
        <input id="f-qty" type="number" placeholder="Initial stock">
        <input id="f-reorder" type="number" placeholder="Reorder level">
        <input id="f-maxorder" type="number" placeholder="Max per order (0=none)">
      </div>
      <button onclick="addMedicine()">Add medicine</button>
      <div id="add-msg" class="msg"></div>
    </div>

    <div class="card">
      <h2>Inventory</h2>
      <table id="inv-table">
        <thead><tr><th>ID</th><th>Code</th><th>Name</th><th>Mfr</th><th>Stock</th><th>Reorder@</th><th>Max/Order</th><th>Adjust</th></tr></thead>
        <tbody></tbody>
      </table>
    </div>

    <div class="card">
      <h2>Recent orders</h2>
      <table id="orders-table">
        <thead><tr><th>Token</th><th>Customer</th><th>Date</th><th>Total</th></tr></thead>
        <tbody></tbody>
      </table>
    </div>

  </div>
</div>

<script>
let PIN = '';

function unlock() {
  PIN = document.getElementById('pin-input').value.trim();
  fetch('/api/staff/medicines', { headers: { 'X-Staff-Pin': PIN } }).then(res => {
    if (!res.ok) {
      document.getElementById('pin-msg').className = 'msg error';
      document.getElementById('pin-msg').textContent = 'Incorrect PIN.';
      return;
    }
    document.getElementById('pin-gate').style.display = 'none';
    document.getElementById('staff-area').style.display = 'block';
    refreshAll();
  });
}

function staffFetch(url, opts) {
  opts = opts || {};
  opts.headers = Object.assign({}, opts.headers, { 'X-Staff-Pin': PIN });
  return fetch(url, opts);
}

async function refreshAll() {
  await loadInventory();
  await loadOrders();
}

async function loadInventory() {
  const res = await staffFetch('/api/staff/medicines');
  const items = await res.json();
  const tbody = document.querySelector('#inv-table tbody');
  tbody.innerHTML = '';
  (items || []).forEach(m => {
    const tr = document.createElement('tr');
    const low = m.Quantity <= m.ReorderLevel;
    tr.innerHTML =
      '<td>' + m.ID + '</td>' +
      '<td>' + escapeHtml(m.Code) + '</td>' +
      '<td>' + escapeHtml(m.Name) + '</td>' +
      '<td>' + escapeHtml(m.Manufacturer) + '</td>' +
      '<td class="' + (low ? 'low' : '') + '">' + m.Quantity + '</td>' +
      '<td>' + m.ReorderLevel + '</td>' +
      '<td>' + (m.MaxPerOrder > 0 ? m.MaxPerOrder : '-') + '</td>' +
      '<td><div class="stock-btns"><button data-id="' + m.ID + '" data-delta="10">+10</button><button data-id="' + m.ID + '" data-delta="-1">-1</button></div></td>';
    tbody.appendChild(tr);
  });
  tbody.querySelectorAll('button').forEach(btn => {
    btn.onclick = () => adjustStock(Number(btn.dataset.id), Number(btn.dataset.delta));
  });
}

async function adjustStock(id, delta) {
  await staffFetch('/api/staff/stock', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ medicineId: id, delta: delta }),
  });
  loadInventory();
}

async function loadOrders() {
  const res = await staffFetch('/api/staff/orders');
  const orders = await res.json();
  const tbody = document.querySelector('#orders-table tbody');
  tbody.innerHTML = '';
  (orders || []).forEach(o => {
    const tr = document.createElement('tr');
    tr.innerHTML =
      '<td>#' + o.ID + '</td>' +
      '<td>' + escapeHtml(o.CustomerName || 'Walk-in') + '</td>' +
      '<td>' + new Date(o.CreatedAt).toLocaleString() + '</td>' +
      '<td>' + o.Total.toFixed(2) + '</td>';
    tbody.appendChild(tr);
  });
}

async function addMedicine() {
  const body = {
    Name: document.getElementById('f-name').value.trim(),
    Code: document.getElementById('f-code').value.trim(),
    Manufacturer: document.getElementById('f-manufacturer').value.trim(),
    Batch: document.getElementById('f-batch').value.trim(),
    ExpiryDate: document.getElementById('f-expiry').value.trim(),
    Price: parseFloat(document.getElementById('f-price').value) || 0,
    Quantity: parseInt(document.getElementById('f-qty').value) || 0,
    ReorderLevel: parseInt(document.getElementById('f-reorder').value) || 0,
    MaxPerOrder: parseInt(document.getElementById('f-maxorder').value) || 0,
  };
  const msg = document.getElementById('add-msg');
  if (!body.Name) {
    msg.className = 'msg error';
    msg.textContent = 'Name is required.';
    return;
  }
  const res = await staffFetch('/api/staff/medicines', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    msg.className = 'msg error';
    msg.textContent = 'Could not add medicine.';
    return;
  }
  msg.className = 'msg ok';
  msg.textContent = 'Added.';
  ['f-name','f-code','f-manufacturer','f-batch','f-expiry','f-price','f-qty','f-reorder','f-maxorder'].forEach(id => {
    document.getElementById(id).value = '';
  });
  loadInventory();
}

function escapeHtml(s) {
  const div = document.createElement('div');
  div.textContent = s == null ? '' : s;
  return div.innerHTML;
}
</script>
</body>
</html>`
