package browser

import "testing"

func idbEval(t *testing.T, p *Page, expr string) string {
	t.Helper()
	v, err := p.Eval(expr)
	if err != nil {
		t.Fatalf("Eval %s: %v", expr, err)
	}
	return v.String()
}

func TestIndexedDBPutGet(t *testing.T) {
	p := flexPage(t, `<script>
	window.out = 'pending';
	var req = indexedDB.open('db', 1);
	req.onupgradeneeded = function(e){ e.target.result.createObjectStore('kv'); };
	req.onsuccess = function(e){
		var db = e.target.result;
		var tx = db.transaction('kv', 'readwrite');
		tx.objectStore('kv').put('hello', 'greeting');
		tx.oncomplete = function(){
			var r = db.transaction('kv').objectStore('kv').get('greeting');
			r.onsuccess = function(){ window.out = r.result; };
		};
	};
	</script>`)
	if got := idbEval(t, p, `window.out`); got != "hello" {
		t.Fatalf("out = %q, want hello", got)
	}
}

func TestIndexedDBKeyPathAndGetAll(t *testing.T) {
	p := flexPage(t, `<script>
	window.count = -1;
	var req = indexedDB.open('db2', 1);
	req.onupgradeneeded = function(e){ e.target.result.createObjectStore('people', {keyPath: 'id'}); };
	req.onsuccess = function(e){
		var db = e.target.result;
		var tx = db.transaction('people', 'readwrite');
		var st = tx.objectStore('people');
		st.put({id: 1, name: 'Ada'});
		st.put({id: 2, name: 'Grace'});
		tx.oncomplete = function(){
			var all = db.transaction('people').objectStore('people').getAll();
			all.onsuccess = function(){ window.count = all.result.length; window.first = all.result[0].name; };
		};
	};
	</script>`)
	if got := idbEval(t, p, `String(window.count)`); got != "2" {
		t.Fatalf("count = %q, want 2", got)
	}
	if got := idbEval(t, p, `window.first`); got != "Ada" {
		t.Fatalf("first = %q, want Ada", got)
	}
}

func TestIndexedDBDeleteAndCount(t *testing.T) {
	p := flexPage(t, `<script>
	window.n = -1;
	var req = indexedDB.open('db3', 1);
	req.onupgradeneeded = function(e){ e.target.result.createObjectStore('s'); };
	req.onsuccess = function(e){
		var db = e.target.result;
		var tx = db.transaction('s','readwrite');
		var st = tx.objectStore('s');
		st.put('a','k1'); st.put('b','k2'); st.delete('k1');
		tx.oncomplete = function(){
			var c = db.transaction('s').objectStore('s').count();
			c.onsuccess = function(){ window.n = c.result; };
		};
	};
	</script>`)
	if got := idbEval(t, p, `String(window.n)`); got != "1" {
		t.Fatalf("count after delete = %q, want 1", got)
	}
}

func TestIndexedDBAddEventListener(t *testing.T) {
	p := flexPage(t, `<script>
	window.via = 'no';
	var req = indexedDB.open('db4', 1);
	req.addEventListener('upgradeneeded', function(e){ e.target.result.createObjectStore('x'); });
	req.addEventListener('success', function(e){
		var db = e.target.result;
		var tx = db.transaction('x','readwrite');
		tx.objectStore('x').put('val','key');
		tx.addEventListener('complete', function(){
			var g = db.transaction('x').objectStore('x').get('key');
			g.addEventListener('success', function(){ window.via = g.result; });
		});
	});
	</script>`)
	if got := idbEval(t, p, `window.via`); got != "val" {
		t.Fatalf("via addEventListener = %q, want val", got)
	}
}

func TestIndexedDBObjectStoreNames(t *testing.T) {
	p := flexPage(t, `<script>
	window.has = false;
	var req = indexedDB.open('db5', 1);
	req.onupgradeneeded = function(e){
		var db = e.target.result;
		db.createObjectStore('alpha');
		window.has = db.objectStoreNames.contains('alpha');
	};
	</script>`)
	if got := idbEval(t, p, `String(window.has)`); got != "true" {
		t.Fatalf("objectStoreNames.contains = %q, want true", got)
	}
}
