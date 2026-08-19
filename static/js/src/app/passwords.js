import zxcvbn from 'zxcvbn';

const StrengthMapping = {
    0: {
        class: 'danger',
        width: '10%',
        status: 'Very Weak'
    },
    1: {
        class: 'danger',
        width: '25%',
        status: 'Very Weak'
    },
    2: {
        class: 'warning',
        width: '50%',
        status: 'Weak'
    },
    3: {
        class: 'success',
        width: '75%',
        status: 'Good'
    },
    4: {
        class: 'success',
        width: '100%',
        status: 'Very Good'
    }
}

const Progress = document.getElementById("password-strength-container")
const ProgressBar = document.getElementById("password-strength-bar")
const StrengthDescription = document.getElementById("password-strength-description")

const updatePasswordStrength = (e) => {
    const candidate = e.target.value
    // If there is no password, clear out the progress bar
    if (!candidate) {
        ProgressBar.style.width = 0
        ProgressBar.setAttribute('aria-valuenow', '0')
        StrengthDescription.textContent = ""
        Progress.classList.add("d-none")
        return
    }
    const score = zxcvbn(candidate).score
    const evaluation = StrengthMapping[score]
    // Update the progress bar
    ProgressBar.classList = `progress-bar bg-${evaluation.class}`
    ProgressBar.style.width = evaluation.width
    ProgressBar.setAttribute('aria-valuenow', String(parseInt(evaluation.width, 10)))
    StrengthDescription.textContent = evaluation.status
    StrengthDescription.classList = `text-${evaluation.class}`
    Progress.classList.remove("d-none")
}

document.getElementById("password").addEventListener("input", updatePasswordStrength)