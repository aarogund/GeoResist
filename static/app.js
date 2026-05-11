async function register() {

  const res = await fetch("/register", {
    method: "POST",
    body: JSON.stringify({
      name: document.getElementById("name").value,
      email: document.getElementById("email").value,
      password: document.getElementById("password").value
    })
  })

  alert(await res.text())
}

async function login() {

  const res = await fetch("/login", {
    method: "POST",
    body: JSON.stringify({
      email: document.getElementById("loginEmail").value,
      password: document.getElementById("loginPassword").value
    })
  })

  const data = await res.json()

  alert("Token: " + data.token)
}