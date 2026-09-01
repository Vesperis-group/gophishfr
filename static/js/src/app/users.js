let users = []

// Save attempts to POST or PUT to /users/
const save = (id) => {
    // Validate that the passwords match
    if (document.getElementById("password").value !== document.getElementById("confirm_password").value) {
        modalError("Passwords must match.")
        return
    }
    let user = {
        username: document.getElementById("username").value,
        password: document.getElementById("password").value,
        role: document.getElementById("role").value,
        password_change_required: document.getElementById("force_password_change_checkbox").checked,
        account_locked: document.getElementById("account_locked_checkbox").checked
    }
    // Submit the user
    if (id != -1) {
        // If we're just editing an existing user,
        // we need to PUT /user/:id
        user.id = id
        api.userId.put(user)
            .done((data) => {
                successFlash("User " + escapeHtml(user.username) + " updated successfully!")
                load()
                dismiss()
                bsModalHide("#modal")
            })
            .fail((data) => {
                modalError(data.responseJSON.message)
            })
    } else {
        // Else, if this is a new user, POST it
        // to /user
        api.users.post(user)
            .done((data) => {
                successFlash("User " + escapeHtml(user.username) + " registered successfully!")
                load()
                dismiss()
                bsModalHide("#modal")
            })
            .fail((data) => {
                modalError(data.responseJSON.message)
            })
    }
}

const dismiss = () => {
    document.getElementById("username").value = ""
    document.getElementById("password").value = ""
    document.getElementById("confirm_password").value = ""
    // No option carries an empty value, so this leaves the select with nothing
    // selected, which is what the previous call did.
    document.getElementById("role").value = ""
    document.getElementById("force_password_change_checkbox").checked = true
    document.getElementById("account_locked_checkbox").checked = false
    // The previous selector matched every element carrying this id, not just
    // the first.
    document.querySelectorAll('[id="modal.flashes"]').forEach((container) => {
        container.replaceChildren()
    })
}

// submitHandler holds the listener currently bound to the modal's submit
// button. The button is reused for every user, so the previous listener has to
// be removed or a single click would save more than once.
let submitHandler = null

const setRole = (slug) => {
    const role = document.getElementById("role")
    role.value = slug
    role.dispatchEvent(new Event("change", { bubbles: true }))
}

const edit = (id) => {
    document.getElementById("username").disabled = false
    const submit = document.getElementById("modalSubmit")
    if (submitHandler) {
        submit.removeEventListener("click", submitHandler)
    }
    submitHandler = () => {
        save(id)
    }
    submit.addEventListener("click", submitHandler)
    if (id == -1) {
        document.getElementById("userModalLabel").textContent = "New User"
        setRole("user")
    } else {
        document.getElementById("userModalLabel").textContent = "Edit User"
        api.userId.get(id)
            .then((user) => {
                document.getElementById("username").value = user.username
                setRole(user.role.slug)
                document.getElementById("force_password_change_checkbox").checked = user.password_change_required
                document.getElementById("account_locked_checkbox").checked = user.account_locked
                if (user.username == "admin") {
                    document.getElementById("username").disabled = true
                }
            }, function () {
                errorFlash("Error fetching user")
            })
    }
}

const deleteUser = (id) => {
    var user = users.find(x => x.id == id)
    if (!user) {
        return
    }
    if (user.username == "admin") {
        Swal.fire({
            title: "Unable to Delete User",
            text: "The user account " + escapeHtml(user.username) + " cannot be deleted.",
            type: "info"
        });
        return
    }
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the account for " + escapeHtml(user.username) + " as well as all of the objects they have created.\n\nThis can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete",
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            return new Promise((resolve, reject) => {
                api.userId.delete(id)
                    .done((msg) => {
                        resolve()
                    })
                    .fail((data) => {
                        reject(data.responseJSON.message)
                    })
            })
            .catch(error => {
                Swal.showValidationMessage(error)
              })
        }
    }).then(function (result) {
        if (result.value){
            Swal.fire(
                'User Deleted!',
                "The user account for " + escapeHtml(user.username) + " and all associated objects have been deleted!",
                'success'
            );
        }
        // Every button whose label contains "OK", which is what the previous
        // selector matched.
        document.querySelectorAll("button").forEach((button) => {
            if (button.textContent.includes("OK")) {
                button.addEventListener('click', function () {
                    location.reload()
                })
            }
        })
    })
}

const impersonate = (id) => {
    var user = users.find(x => x.id == id)
    if (!user) {
        return
    }
    Swal.fire({
        title: "Are you sure?",
        html: "You will be logged out of your account and logged in as <strong>" + escapeHtml(user.username) + "</strong>",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Swap User",
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
    }).then((result) => {
        if (result.value) {

         fetch('/impersonate', {
                method: 'post',
                body: "username=" + user.username + "&csrf_token=" + encodeURIComponent(csrf_token),
                headers: {
                    'Content-Type': 'application/x-www-form-urlencoded',
                  },
          }).then((response) => {
                if (response.status == 200) {
                    Swal.fire({
                        title: "Success!",
                        html: "Successfully changed to user <strong>" + escapeHtml(user.username) + "</strong>.",
                        type: "success",
                        showCancelButton: false,
                        confirmButtonText: "Home",
                        allowOutsideClick: false,
                    }).then((result) => {
                        if (result.value) {
                            window.location.href = "/"
                        }});
                } else {
                    Swal.fire({
                        title: "Error!",
                        type: "error",
                        html: "Failed to change to user <strong>" + escapeHtml(user.username) + "</strong>.",
                        showCancelButton: false,
                    })
                }
            })
        }
      })
}

const load = () => {
    document.getElementById("userTable").style.display = "none"
    document.getElementById("loading").style.display = ""
    api.users.get()
        .then((us) => {
            users = us
            document.getElementById("loading").style.display = "none"
            document.getElementById("userTable").style.display = ""
            let userTable = new DataTable("#userTable", {
                destroy: true,
                columnDefs: [{
                    orderable: false,
                    targets: "no-sort"
                }]
            });
            userTable.clear();
            userRows = []
            users.forEach((user) => {
                lastlogin = ""
                if (user.last_login != "0001-01-01T00:00:00Z") {
                    lastlogin = moment(user.last_login).format('MMMM Do YYYY, h:mm:ss a')
                }
                userRows.push([
                    escapeHtml(user.username),
                    escapeHtml(user.role.name),
                    lastlogin,
                    "<div class='float-end'>\
                    <button class='btn btn-warning impersonate_button' data-user-id='" + user.id + "'>\
                    <i class='fa fa-retweet'></i>\
                    </button>\
                    <button class='btn btn-primary edit_button' data-bs-toggle='modal' data-bs-target='#modal' data-user-id='" + user.id + "'>\
                    <i class='fa fa-pencil'></i>\
                    </button>\
                    <button class='btn btn-danger delete_button' data-user-id='" + user.id + "'>\
                    <i class='fa fa-trash-o'></i>\
                    </button></div>"
                ])
            })
            userTable.rows.add(userRows).draw();
        }, () => {
            errorFlash("Error fetching users")
        })
}

// The application scripts are plain classic scripts at the end of <body>, so
// the document is still parsing when they run and DOMContentLoaded has not
// fired yet.
document.addEventListener('DOMContentLoaded', function () {
    load()
    // Setup the event listeners
    document.getElementById('modal').addEventListener('hide.bs.modal', function () {
        dismiss();
    });
    document.getElementById("new_button").addEventListener("click", function () {
        edit(-1)
    })
    // Rows are rebuilt whenever the table is redrawn, so these stay delegated
    // and resolve the clicked button rather than the table.
    document.getElementById("userTable").addEventListener('click', function (e) {
        const button = e.target.closest('.edit_button, .delete_button, .impersonate_button')
        if (!button || !this.contains(button)) {
            return
        }
        const userId = button.getAttribute('data-user-id')
        if (button.classList.contains('edit_button')) {
            edit(userId)
            return
        }
        if (button.classList.contains('delete_button')) {
            deleteUser(userId)
            return
        }
        impersonate(userId)
    })
});
